package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// 判题的候选账号选择、答案比对提示与严格 JSON 判定复用参考项目 quality_judge.go。
// 传输接入本项目已有网关，保留各平台的鉴权、代理和模型协议转换，不迁入账号处置模块。
func NewConversationQualityJudgeService(accounts AccountRepository, groups GroupRepository, slots *ConcurrencyService,
	openai *OpenAIGatewayService, gateway *GatewayService, gemini *GeminiMessagesCompatService, antigravity *AntigravityGatewayService,
) *ConversationQualityJudgeService {
	s := &ConversationQualityJudgeService{accounts: accounts, groups: groups, slots: slots}
	s.request = func(ctx context.Context, accountID int64, model, prompt string) (string, error) {
		account, err := accounts.GetByID(ctx, accountID)
		if err != nil || account == nil || !account.IsSchedulable() {
			return "", fmt.Errorf("判题账号不可用")
		}
		body, err := json.Marshal(map[string]any{"model": model, "stream": true, "max_tokens": 32000,
			"messages": []any{map[string]any{"role": "user", "content": prompt}}})
		if err != nil {
			return "", err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1/messages", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		request.Header.Set("Content-Type", "application/json")
		writer := &openAIWSLatencyProbeWriter{header: make(http.Header), started: time.Now(), firstMS: -1,
			closed: make(chan bool), capture: true}
		c := &gin.Context{Request: request, Writer: writer}
		switch {
		case account.Platform == PlatformGemini:
			_, err = gemini.Forward(ctx, c, account, body)
		case account.Platform == PlatformAntigravity && account.Type != AccountTypeAPIKey:
			_, err = antigravity.Forward(ctx, c, account, body, false)
		case account.Platform == PlatformAnthropic:
			var parsed *ParsedRequest
			parsed, err = ParseGatewayRequest(NewRequestBodyRef(body), APIProtocolAnthropic)
			if err == nil {
				_, err = gateway.Forward(ctx, c, account, parsed)
			}
		default:
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			_, err = openai.ForwardAsAnthropic(ctx, c, account, body, "", "")
		}
		if err != nil || ctx.Err() != nil || writer.Status() >= 400 || writer.failed || writer.overflow || !writer.completed || strings.TrimSpace(writer.output.String()) == "" {
			return "", fmt.Errorf("判题请求未完整完成")
		}
		return writer.output.String(), nil
	}
	return s
}

func (s *OpenAIGatewayService) SetConversationQualityJudge(judge *ConversationQualityJudgeService) {
	if s != nil {
		s.conversationQualityJudge = judge
	}
}
