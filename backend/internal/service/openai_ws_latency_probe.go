package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// 探测目标是创建并筛选可接管的独立上游会话，而非只比较本系统账号的平均速度。
// 后台只探测显式启用的 OpenAI API Key 账号；领取时按当前调用分组重新校验权限。
// 同一账号可以创建多个探测会话；每路都应使用独立的上游会话标识，并把该标识与结果一起保留。
// 上游可能把这些会话分配到不同内部账号；按用户确认的规则，以实测首字延迟筛选即可，
// 不要求确认内部 OpenAI 账号编号，也不要求先填满某个内部账号的 15 个并发。
type openAIWSLatencyProbeResult struct {
	resultID            string
	upstreamSessionID   string
	upstreamModel       string
	probeNumber         int
	requestedAccountID  int64
	accountID           int64
	attemptedAccountIDs []int64
	failure             string
	ms                  int
	ok                  bool
	timedOut            bool
	responseStatus      int // 仅供备用池诊断，不改变成功判定。
	qualityJudgment     *QualityJudgment
	qualityQuestion     *conversationQualityQuestion
}

type openAIWSLatencyProbeSessionIDContextKey struct{}

func openAIWSLatencyProbeSessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sessionID, _ := ctx.Value(openAIWSLatencyProbeSessionIDContextKey{}).(string)
	return strings.TrimSpace(sessionID)
}

// 候选速度规则与「用户慢请求的触发阈值」分开：
// 探测成功且首字严格低于 10 秒才合格，不再要求比原会话快 20% 或至少快 250 毫秒，
// 也不把用户请求的可选最低输出 TPS（每秒输出词元数）作为候选探测的额外速度门槛。
// 一轮内优先选最快的合格会话；任何一路低于 5 秒时允许直接选中，无须等待全部返回。
// 没有合格会话才新建下一轮独立会话，最多探测 5 轮；并发数使用配置值，默认 6，
// 它表示会话数而非不同账号数。已产生的其余合格会话应按复用保留时间进入共享池。
const (
	openAIWSLatencyCandidateMaxFirstTokenMS = 10_000
	openAIWSLatencyFastCandidateMS          = 5_000
	openAIWSLatencyMaxProbeRounds           = 5
)

func (s *OpenAIGatewayService) forwardOpenAIWSLatencyProbe(ctx context.Context, account *Account, model string, requiredTransport OpenAIUpstreamTransport) (openAIWSLatencyProbeResult, error) {
	return s.forwardOpenAIWSLatencyProbeWithRelease(ctx, account, model, requiredTransport, nil)
}

// 答题网络请求结束后即可归还被测账号名额；后续判题只占用判题账号名额。
// 调用方仍负责用 defer 处理发出网络请求之前就失败的情况。
func (s *OpenAIGatewayService) forwardOpenAIWSLatencyProbeWithRelease(ctx context.Context, account *Account, model string, requiredTransport OpenAIUpstreamTransport, releaseSlot func()) (openAIWSLatencyProbeResult, error) {
	observed := openAIWSLatencyProbeResult{accountID: account.ID, upstreamSessionID: openAIWSLatencyProbeSessionID(ctx)}
	quality, configErr := account.OpenAISessionQualityConfig()
	if configErr != nil && isOpenAIPreparedProbe(ctx) {
		return observed, configErr
	}
	qualityEnabled := isOpenAIPreparedProbe(ctx) && quality != nil && quality.Enabled
	if qualityEnabled {
		observed.ms = -1 // 没有测到首字时不能在诊断中显示为 0 毫秒。
	}
	timeout := openAIWSLatencyProbeTimeout
	if qualityEnabled {
		timeout = openAISessionQualityProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// 探测只发送合成测试输入，不复制真实用户消息、工具结果、旧响应引用或旧会话标识。
	// 使用本路新生成的会话标识发送探测，并保留该标识供用户接管；本地结果编号不能替代它。
	// 探测会占用上游额度，但不计入用户账单；接管时必须另行发送用户的完整有效上下文。
	probeBody := map[string]any{"model": model, "stream": true, "store": false, "max_output_tokens": 64, "input": []any{map[string]any{"role": "user", "content": "Reply with the single word OK."}}}
	if qualityEnabled {
		if err := validateConversationQualityConfig(quality); err != nil {
			return observed, err
		}
		question := &conversationQualityQuestion{Prompt: quality.Prompt, ExpectedAnswer: quality.ExpectedAnswer}
		if quality.QuestionKind == QualityRandomQuestionKind {
			questions, err := generateQualityRandomSamples(1, rand.Reader)
			if err != nil {
				return observed, err
			}
			question = questions[0]
		}
		observed.qualityQuestion = question
		probeBody["input"] = []any{map[string]any{"role": "user", "content": question.Prompt + "\n\n只输出最终答案，不要解释。"}}
		// 与参考项目普通质量规则一致，为推理和完整答案留出空间。
		delete(probeBody, "max_output_tokens")
		if quality.ReasoningEffort != "" {
			probeBody["reasoning"] = map[string]any{"effort": quality.ReasoningEffort}
		}
	}
	if observed.upstreamSessionID != "" {
		probeBody["prompt_cache_key"] = observed.upstreamSessionID
	}
	body, _ := json.Marshal(probeBody)
	request, err := http.NewRequestWithContext(probeCtx, http.MethodPost, "http://localhost/v1/responses", bytes.NewReader(body))
	if err != nil {
		return observed, err
	}
	applyOpenAICodexProbeHeaders(request.Header)
	if observed.upstreamSessionID != "" {
		request.Header.Set("session_id", observed.upstreamSessionID)
		request.Header.Set("conversation_id", observed.upstreamSessionID)
	}
	writer := &openAIWSLatencyProbeWriter{header: make(http.Header), mode: s.openAITTFTMode(ctx), started: time.Now(), firstMS: -1, closed: make(chan bool), capture: qualityEnabled}
	c := &gin.Context{Request: request, Writer: writer}
	if observed.upstreamSessionID != "" {
		c.Set(openAIWSLatencyUpstreamSessionContextKey, observed.upstreamSessionID)
		c.Set(openAIPreparedSessionContextKey, isOpenAIPreparedProbe(probeCtx))
	}
	if requiredTransport == OpenAIUpstreamTransportHTTPSSE {
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	} else {
		SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	}
	result, forwardErr := s.Forward(probeCtx, c, account, body)
	if releaseSlot != nil {
		releaseSlot()
	}
	if result != nil {
		observed.upstreamModel = strings.TrimSpace(result.UpstreamModel)
	}
	if observed.upstreamModel == "" {
		observed.upstreamModel = model
	}
	if writer.firstMS >= 0 && (forwardErr == nil || errors.Is(probeCtx.Err(), context.DeadlineExceeded)) {
		observed.ms, observed.ok = writer.firstMS, true
	} else if forwardErr == nil && result != nil && result.SucceededForScheduling() && result.FirstTokenMs != nil {
		observed.ms, observed.ok = *result.FirstTokenMs, true
	} else if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		observed.timedOut = true
		if !qualityEnabled {
			observed.ms = int(time.Since(writer.started).Milliseconds())
		}
	}
	observed.responseStatus = writer.status // 未写出响应时为零，不能以默认 200 误报成功。
	if writer.failed || writer.Status() >= 400 {
		observed.ok = false
	}
	if qualityEnabled {
		if writer.firstMS >= 0 {
			observed.ms = writer.firstMS
		}
		observed.qualityJudgment = &QualityJudgment{Verdict: "unknown", Reason: "answer_incomplete"}
		if forwardErr == nil && probeCtx.Err() == nil && observed.ok && writer.completed && !writer.failed && !writer.overflow && strings.TrimSpace(writer.output.String()) != "" {
			observed.qualityJudgment = s.conversationQualityJudge.Judge(probeCtx, account.ID, quality, observed.qualityQuestion.ExpectedAnswer, writer.output.String())
		}
		// 首字快不代表答题完成；超时、拒答、截断、判题未知均不能进池。
		if observed.qualityJudgment == nil || observed.qualityJudgment.Verdict != "correct" {
			observed.ok = false
		}
	}
	return observed, forwardErr
}

// Discards generated text and retains at most one bounded SSE line. The timing
// uses the site's normal TTFT definition, including its visible-output option.
type openAIWSLatencyProbeWriter struct {
	header       http.Header
	status       int
	size         int
	line         []byte
	mode         string
	started      time.Time
	firstMS      int
	failed       bool
	closed       chan bool
	capture      bool
	completed    bool
	overflow     bool
	lineOverflow bool
	stopReason   string
	output       strings.Builder
}

func (w *openAIWSLatencyProbeWriter) Header() http.Header { return w.header }
func (w *openAIWSLatencyProbeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *openAIWSLatencyProbeWriter) WriteHeaderNow() { w.WriteHeader(http.StatusOK) }
func (w *openAIWSLatencyProbeWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
func (w *openAIWSLatencyProbeWriter) Size() int {
	if !w.Written() {
		return -1
	}
	return w.size
}
func (w *openAIWSLatencyProbeWriter) Written() bool            { return w.status != 0 }
func (w *openAIWSLatencyProbeWriter) Flush()                   { w.WriteHeaderNow() }
func (w *openAIWSLatencyProbeWriter) CloseNotify() <-chan bool { return w.closed }
func (w *openAIWSLatencyProbeWriter) Pusher() http.Pusher      { return nil }
func (w *openAIWSLatencyProbeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, fmt.Errorf("latency probe does not support hijacking")
}
func (w *openAIWSLatencyProbeWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *openAIWSLatencyProbeWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	w.size += len(data)
	for _, b := range data {
		if b != '\n' {
			if len(w.line) < 65536 {
				w.line = append(w.line, b)
			} else {
				w.lineOverflow = true
			}
			continue
		}
		line := strings.TrimSpace(string(w.line))
		w.line = w.line[:0]
		if w.lineOverflow {
			w.lineOverflow = false
			if w.capture {
				w.overflow = true
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		eventType := gjson.Get(payload, "type").String()
		if eventType == "error" || eventType == "response.failed" {
			w.failed = true
		}
		if w.firstMS < 0 && openAIStreamDataStartsTTFT(payload, eventType, false, w.mode) {
			w.firstMS = int(time.Since(w.started).Milliseconds())
		}
		if w.capture {
			w.captureOutput(payload, eventType)
		}
	}
	return len(data), nil
}

func (w *openAIWSLatencyProbeWriter) captureOutput(payload, eventType string) {
	appendText := func(text string) {
		if w.output.Len()+len(text) > 64000 {
			w.overflow = true
			return
		}
		w.output.WriteString(text)
	}
	switch eventType {
	case "response.output_text.delta":
		appendText(gjson.Get(payload, "delta").String())
	case "response.refusal.delta", "response.incomplete":
		w.failed = true
	case "response.completed":
		status := gjson.Get(payload, "response.status").String()
		w.completed = status == "" || status == "completed"
		if !w.completed {
			w.failed = true
		}
		// 最终结果可能只在此处标明拒答；即使已收到正文增量，也必须检查。
		captureFinalText := w.output.Len() == 0
		for _, item := range gjson.Get(payload, "response.output").Array() {
			for _, content := range item.Get("content").Array() {
				if content.Get("type").String() == "refusal" {
					w.failed = true
				}
				if captureFinalText && content.Get("type").String() == "output_text" {
					appendText(content.Get("text").String())
				}
			}
		}
	case "content_block_delta":
		if gjson.Get(payload, "delta.type").String() == "text_delta" {
			appendText(gjson.Get(payload, "delta.text").String())
		}
	case "message_delta":
		w.stopReason = gjson.Get(payload, "delta.stop_reason").String()
	case "message_stop":
		w.completed = w.stopReason == "end_turn"
		if !w.completed {
			w.failed = true
		}
	}
}
