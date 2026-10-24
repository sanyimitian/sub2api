package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	accountTestGenerationPromptKey = "account_test_generation_prompt"
	accountTestGenerationMaxTokens = 8192
	accountTestGenerationMaxBytes  = 4 << 20
)

func accountTestGenerationPrompt(c *gin.Context) string {
	return c.GetString(accountTestGenerationPromptKey)
}

// Generation uses the existing account-specific transport but must not mistake
// a synthetic/media connectivity probe for a response to the user's prompt.
func validateAccountTestGeneration(account *Account, modelID string) error {
	model := strings.ToLower(account.GetMappedModel(strings.TrimSpace(modelID)))
	if account.IsSyntheticUITest() || account.IsTypeSafe() {
		return errors.New("This account does not support text generation tests")
	}
	if isOpenAIImageModel(model) || isImageGenerationModel(model) || isGrokImageGenerationModel(model) ||
		isGrokVideoGenerationModel(model) || strings.Contains(model, "embedding") ||
		strings.Contains(model, "rerank") || strings.Contains(model, "tts") ||
		strings.Contains(model, "transcri") || strings.Contains(model, "whisper") ||
		strings.Contains(model, "realtime") || strings.Contains(model, "voice") ||
		strings.Contains(model, "audio") || strings.HasPrefix(model, "sora") || strings.HasPrefix(model, "dall-e") {
		return fmt.Errorf("Model %q is not supported for text generation tests; select a text model", modelID)
	}
	return nil
}

// Only scheduled generation requests get the larger output budget. Ordinary
// connectivity and media probes retain their existing requests and limits.
func applyAccountTestGenerationPayload(c *gin.Context, payload map[string]any, protocol string, isOAuth bool) {
	prompt := accountTestGenerationPrompt(c)
	if prompt == "" {
		return
	}
	switch protocol {
	case APIProtocolAnthropic:
		payload["messages"] = []map[string]any{{"role": "user", "content": []map[string]any{{"type": "text", "text": prompt}}}}
		payload["max_tokens"] = accountTestGenerationMaxTokens
	case APIProtocolChatCompletions:
		payload["messages"] = []map[string]any{{"role": "user", "content": prompt}}
		// New OpenAI reasoning models use max_completion_tokens; compatible
		// providers normally expect max_tokens.
		model, _ := payload["model"].(string)
		if accountTestUsesMaxCompletionTokens(model) {
			payload["max_completion_tokens"] = accountTestGenerationMaxTokens
		} else {
			payload["max_tokens"] = accountTestGenerationMaxTokens
		}
	case APIProtocolResponses:
		payload["input"] = []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}}}}
		// Codex OAuth rejects max_output_tokens. Its default output budget is
		// sufficient, while the public Responses API accepts an explicit limit.
		if !isOAuth {
			payload["max_output_tokens"] = accountTestGenerationMaxTokens
		}
	}
}

func accountTestUsesMaxCompletionTokens(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	model = model[strings.LastIndex(model, "/")+1:]
	if strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4") {
		return true
	}
	version, ok := strings.CutPrefix(model, "gpt-")
	if !ok {
		return false
	}
	major, _ := strconv.Atoi(strings.SplitN(strings.SplitN(version, ".", 2)[0], "-", 2)[0])
	return major >= 5
}

// The generation parser requires the provider's actual completion marker and
// rejects length limits, safety blocks, errors and abrupt disconnects. Partial
// text is still emitted so failed results remain useful for diagnosis.
func (s *AccountTestService) processAccountTestGenerationStream(c *gin.Context, body io.Reader, protocol string) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), accountTestGenerationMaxBytes)
	textBytes := 0
	hasText, finished := false, false
	emit := func(value string) error {
		textBytes += len(value)
		if textBytes > accountTestGenerationMaxBytes {
			return errors.New("Generation response exceeds the 4 MiB limit")
		}
		if strings.TrimSpace(value) != "" {
			hasText = true
		}
		if value != "" {
			s.sendEvent(c, TestEvent{Type: "content", Text: value})
		}
		return nil
	}
	complete := func() error {
		if err := c.Request.Context().Err(); err != nil {
			return s.sendErrorAndEnd(c, err.Error())
		}
		if !hasText {
			return s.sendErrorAndEnd(c, "Generation returned an empty response")
		}
		s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
		return nil
	}
	for scanner.Scan() {
		if err := c.Request.Context().Err(); err != nil {
			return s.sendErrorAndEnd(c, err.Error())
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if finished {
				return complete()
			}
			return s.sendErrorAndEnd(c, "Generation stream ended before completion")
		}
		if !gjson.Valid(data) {
			return s.sendErrorAndEnd(c, "Invalid JSON in generation stream")
		}
		message := gjson.Parse(data)
		if failure := message.Get("error"); failure.Exists() && failure.Type != gjson.Null {
			detail := failure.Get("message").String()
			if detail == "" {
				detail = failure.String()
			}
			return s.sendErrorAndEnd(c, "Generation failed: "+detail)
		}
		var content string
		switch protocol {
		case APIProtocolResponses:
			switch message.Get("type").String() {
			case "response.output_text.delta":
				content = message.Get("delta").String()
			case "response.failed", "response.incomplete", "error":
				detail := message.Get("response.error.message").String()
				if detail == "" {
					detail = message.Get("response.incomplete_details.reason").String()
				}
				if detail == "" {
					detail = message.Get("message").String()
				}
				return s.sendErrorAndEnd(c, "Generation failed or incomplete: "+detail)
			case "response.completed", "response.done":
				status := message.Get("response.status").String()
				if status != "" && status != "completed" {
					return s.sendErrorAndEnd(c, "Generation did not complete: "+status)
				}
				// Some providers send the whole answer only in the final event.
				if textBytes == 0 {
					for _, output := range message.Get("response.output").Array() {
						for _, part := range output.Get("content").Array() {
							if part.Get("type").String() == "output_text" {
								content += part.Get("text").String()
							}
						}
					}
				}
				finished = true
			}
		case APIProtocolAnthropic:
			switch message.Get("type").String() {
			case "content_block_start":
				if message.Get("content_block.type").String() == "text" {
					content = message.Get("content_block.text").String()
				}
			case "content_block_delta":
				content = message.Get("delta.text").String()
			case "message_delta":
				if reason := message.Get("delta.stop_reason").String(); reason != "" && reason != "end_turn" && reason != "stop_sequence" {
					return s.sendErrorAndEnd(c, "Generation stopped before completion: "+reason)
				}
			case "message_stop":
				finished = true
			}
		case APIProtocolChatCompletions:
			for _, choice := range message.Get("choices").Array() {
				if choice.Get("index").Int() != 0 {
					continue
				}
				content += choice.Get("delta.content").String()
				if textBytes == 0 && content == "" {
					content += choice.Get("message.content").String()
				}
				if reason := choice.Get("finish_reason").String(); reason != "" {
					if reason != "stop" {
						if err := emit(content); err != nil {
							return s.sendErrorAndEnd(c, err.Error())
						}
						return s.sendErrorAndEnd(c, "Generation stopped before completion: "+reason)
					}
					finished = true
				}
			}
		case "gemini":
			if wrapped := message.Get("response"); wrapped.Exists() {
				message = wrapped
			}
			if failure := message.Get("error"); failure.Exists() && failure.Type != gjson.Null {
				return s.sendErrorAndEnd(c, "Generation failed: "+failure.Get("message").String())
			}
			if reason := message.Get("promptFeedback.blockReason").String(); reason != "" {
				return s.sendErrorAndEnd(c, "Generation was blocked: "+reason)
			}
			candidate := message.Get("candidates.0")
			for _, part := range candidate.Get("content.parts").Array() {
				if !part.Get("thought").Bool() {
					content += part.Get("text").String()
				}
			}
			if reason := candidate.Get("finishReason").String(); reason != "" {
				if reason != "STOP" {
					if err := emit(content); err != nil {
						return s.sendErrorAndEnd(c, err.Error())
					}
					return s.sendErrorAndEnd(c, "Generation stopped before completion: "+reason)
				}
				finished = true
			}
		default:
			return s.sendErrorAndEnd(c, "Unsupported generation stream protocol")
		}
		if err := emit(content); err != nil {
			return s.sendErrorAndEnd(c, err.Error())
		}
		if finished {
			return complete()
		}
	}
	if err := scanner.Err(); err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Generation stream read error: %s", err))
	}
	if err := c.Request.Context().Err(); err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	return s.sendErrorAndEnd(c, "Generation stream ended before completion")
}

func (s *AccountTestService) processAccountTestGenerationJSON(c *gin.Context, body []byte, protocol string) error {
	if len(body) > accountTestGenerationMaxBytes {
		return s.sendErrorAndEnd(c, "Generation response exceeds the 4 MiB limit")
	}
	if !gjson.ValidBytes(body) {
		return s.sendErrorAndEnd(c, "Invalid generation response JSON")
	}
	if protocol != APIProtocolAnthropic {
		return s.sendErrorAndEnd(c, "Unsupported generation response protocol")
	}
	response := gjson.ParseBytes(body)
	var text strings.Builder
	for _, part := range response.Get("content").Array() {
		if part.Get("type").String() == "text" {
			text.WriteString(part.Get("text").String())
		}
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: text.String()})
	reason := response.Get("stop_reason").String()
	if reason != "end_turn" && reason != "stop_sequence" {
		return s.sendErrorAndEnd(c, "Generation stopped before completion: "+reason)
	}
	if strings.TrimSpace(text.String()) == "" {
		return s.sendErrorAndEnd(c, "Generation returned an empty response")
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

// Antigravity's normal test deliberately requests a single token. Generation
// retains its native envelope, credentials and retry handling with a real prompt.
func (s *AccountTestService) testAntigravityGeneration(c *gin.Context, account *Account, modelID string) error {
	gateway := s.antigravityGatewayService
	ctx := c.Request.Context()
	if gateway == nil || gateway.tokenProvider == nil || gateway.httpUpstream == nil {
		return s.sendErrorAndEnd(c, "Antigravity generation service not configured")
	}
	accessToken, err := gateway.tokenProvider.GetAccessToken(ctx, account)
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	projectID, err := resolveAntigravityProjectID(account)
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	mappedModel := gateway.getMappedModel(account, modelID)
	if mappedModel == "" {
		return s.sendErrorAndEnd(c, fmt.Sprintf("model %s not in whitelist", modelID))
	}
	var body []byte
	if strings.HasPrefix(mappedModel, "gemini-") {
		body, err = gateway.buildGeminiTestRequest(projectID, mappedModel)
	} else {
		body, err = gateway.buildClaudeTestRequest(projectID, mappedModel)
	}
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return s.sendErrorAndEnd(c, "Invalid Antigravity request envelope")
	}
	request, ok := envelope["request"].(map[string]any)
	if !ok {
		return s.sendErrorAndEnd(c, "Invalid Antigravity request envelope")
	}
	request["contents"] = []map[string]any{{"role": "user", "parts": []map[string]any{{"text": accountTestGenerationPrompt(c)}}}}
	generationConfig, _ := request["generationConfig"].(map[string]any)
	if generationConfig == nil {
		generationConfig = make(map[string]any)
		request["generationConfig"] = generationConfig
	}
	generationConfig["maxOutputTokens"] = accountTestGenerationMaxTokens
	body, _ = json.Marshal(envelope)
	proxyURL := ""
	if account.Proxy != nil && account.ProxyID != nil {
		proxyURL = account.Proxy.URL()
	}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: modelID})
	result, err := gateway.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx: ctx, prefix: "[antigravity-generation-test]", account: account,
		proxyURL: proxyURL, accessToken: accessToken, action: "streamGenerateContent",
		body: body, httpUpstream: gateway.httpUpstream, settingService: gateway.settingService,
		accountRepo: gateway.accountRepo, requestedModel: modelID, handleError: testConnectionHandleError,
	})
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	if result == nil || result.resp == nil {
		return s.sendErrorAndEnd(c, "Antigravity returned an empty response")
	}
	defer result.resp.Body.Close()
	if result.resp.StatusCode != http.StatusOK {
		failureBody, _ := io.ReadAll(io.LimitReader(result.resp.Body, 64<<10))
		return s.sendErrorAndEnd(c, fmt.Sprintf("API returned %d: %s", result.resp.StatusCode, failureBody))
	}
	return s.processAccountTestGenerationStream(c, result.resp.Body, "gemini")
}

// RunGenerationBackground executes a full prompt for an independent degradation test.
func (s *AccountTestService) RunGenerationBackground(ctx context.Context, accountID int64, modelID string, prompt string) (*DegradationTestResult, error) {
	startedAt := time.Now()

	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = (&http.Request{}).WithContext(ctx)
	ginCtx.Set(accountTestBackgroundKey, true)
	ginCtx.Set(accountTestGenerationPromptKey, prompt)

	testErr := s.TestAccountConnection(ginCtx, accountID, modelID, prompt, AccountTestModeDefault)

	finishedAt := time.Now()
	body := w.Body.String()
	responseText, errMsg := parseGenerationTestSSEOutput(body)
	if ctx.Err() != nil {
		testErr = ctx.Err()
	}
	if testErr == nil && errMsg == "" && prompt != "" && strings.TrimSpace(responseText) == "" {
		errMsg = "Generation returned an empty response"
	}

	status := "success"
	if testErr != nil || errMsg != "" {
		status = "failed"
		if testErr != nil {
			errMsg = testErr.Error()
		}
	}

	return &DegradationTestResult{
		Status:       status,
		ResponseText: responseText,
		ErrorMessage: errMsg,
		LatencyMs:    finishedAt.Sub(startedAt).Milliseconds(),
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
	}, nil
}

// parseGenerationTestSSEOutput extracts response text and error message from captured SSE output.
func parseGenerationTestSSEOutput(body string) (responseText, errMsg string) {
	var texts []string
	completed := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")
		var event TestEvent
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			continue
		}
		switch event.Type {
		case "content":
			if event.Text != "" {
				texts = append(texts, event.Text)
			}
		case "error":
			errMsg = event.Error
			if errMsg == "" {
				errMsg = "Account test failed"
			}
		case "test_complete":
			completed = event.Success
		}
	}
	responseText = strings.Join(texts, "")
	if !completed && errMsg == "" {
		errMsg = "Stream ended without successful completion"
	}
	return
}
