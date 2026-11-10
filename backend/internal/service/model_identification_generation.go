package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type degradationGroupTester interface {
	RunGroupGeneration(context.Context, *ModelIdentificationTarget, string, string, string) (*DegradationTestResult, error)
}

// 通过管理员配置的 URL/API Key 正常调用，路由、额度与计费由目标服务处理。
type GroupGenerationService struct {
	encryptor SecretEncryptor
	client    *http.Client
}

func NewGroupGenerationService(encryptor SecretEncryptor) *GroupGenerationService {
	timeout := time.Duration(DegradationTestMaxTimeoutSeconds) * time.Second
	client := newSSRFSafeHTTPClientWithHeaderTimeout(timeout, timeout)
	// 禁止重定向，避免凭证随自定义认证头转发到另一个站点。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &GroupGenerationService{encryptor: encryptor, client: client}
}
func (s *GroupGenerationService) RunGroupGeneration(ctx context.Context, target *ModelIdentificationTarget, model, prompt, effort string) (*DegradationTestResult, error) {
	if target == nil {
		return nil, errors.New("缺少本次检测的配置快照")
	}
	if target.Endpoint == "" || target.EncryptedAPIKey == "" {
		return nil, errors.New("请管理员补齐检测地址和密钥")
	}
	key, err := s.encryptor.Decrypt(target.EncryptedAPIKey)
	if err != nil {
		return nil, errors.New("无法解密检测密钥")
	}
	if err = validateIdentificationProtocol(target.Protocol, effort); err != nil {
		return nil, err
	}
	started := time.Now()
	result := &DegradationTestResult{Status: "failed", StartedAt: started, ReasoningEffort: effort}
	defer func() { result.FinishedAt = time.Now(); result.LatencyMs = time.Since(started).Milliseconds() }()
	req, err := identificationGenerationRequest(ctx, target, key, model, prompt, effort)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// net/http 错误包含完整 URL，不能存入公开结果或日志。
		return nil, errors.New("无法连接检测接口，请检查地址、证书及网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.ErrorMessage = fmt.Sprintf("检测接口返回 HTTP %d", resp.StatusCode)
		return result, nil
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req
	c.Set(accountTestBackgroundKey, true)
	c.Set(accountTestErrorRedactorKey, identificationRedactor(target.Endpoint, key))
	// 仅复用协议解析器；绝不进入内部账号选择器或连接探针。
	parser := &AccountTestService{}
	err = parser.processAccountTestGenerationStream(c, io.LimitReader(resp.Body, 8<<20), target.Protocol)
	result.ResponseText, result.ErrorMessage = parseGenerationTestSSEOutput(recorder.Body.String())
	if err != nil {
		result.ErrorMessage = err.Error()
	}
	if ctx.Err() != nil {
		result.ErrorMessage = ctx.Err().Error()
	}
	if err == nil && result.ErrorMessage == "" {
		result.Status = "success"
	}
	// 在落库前处理上游回显，轮换密钥后旧作品也不会泄露旧密钥。
	clean := identificationRedactor(target.Endpoint, key)
	if clean(result.ResponseText) != result.ResponseText {
		result.Status = "failed"
		result.ResponseText = ""
		result.ErrorMessage = "检测接口返回了敏感配置信息，已隐藏本次内容"
	}
	result.ErrorMessage = clean(result.ErrorMessage)
	return result, nil
}
func identificationGenerationRequest(ctx context.Context, target *ModelIdentificationTarget, key, model, prompt, effort string) (*http.Request, error) {
	payload := map[string]any{"model": model, "stream": true}
	path := ""
	headers := map[string]string{"Authorization": "Bearer " + key}
	switch target.Protocol {
	case APIProtocolResponses:
		path = "/v1/responses"
	case APIProtocolChatCompletions:
		path = "/v1/chat/completions"
	case APIProtocolAnthropic:
		path = "/v1/messages"
		headers = map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}
	case "gemini":
		path = "/v1beta/models/" + url.PathEscape(strings.TrimPrefix(model, "models/")) + ":streamGenerateContent?alt=sse"
		headers = map[string]string{"x-goog-api-key": key}
		payload = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}}, "generationConfig": map[string]any{"maxOutputTokens": accountTestGenerationMaxTokens}}
	default:
		return nil, errors.New("不支持的检测接口类型")
	}
	if target.Protocol != "gemini" {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set(accountTestGenerationPromptKey, prompt)
		c.Set(accountTestGenerationEffortKey, effort)
		if err := applyAccountTestGenerationPayload(c, payload, target.Protocol, false); err != nil {
			return nil, err
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("无法构建检测请求")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(target.Endpoint, path), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("检测接口地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}
func identificationRedactor(endpoint, key string) func(string) string {
	values := []string{key, endpoint}
	if u, err := url.Parse(endpoint); err == nil {
		values = append(values, u.Host)
	}
	pairs := make([]string, 0)
	for _, value := range values {
		if value == "" {
			continue
		}
		encoded, _ := json.Marshal(value)
		for _, variant := range []string{value, url.QueryEscape(value), url.PathEscape(value), html.EscapeString(value), string(encoded[1 : len(encoded)-1])} {
			pairs = append(pairs, variant, "[已隐藏]")
		}
	}
	return strings.NewReplacer(pairs...).Replace
}
