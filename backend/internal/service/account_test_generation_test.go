//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type generationTestUpstream struct {
	response  *http.Response
	err       error
	accountID int64
	request   *http.Request
	body      []byte
	calls     int
}

func (u *generationTestUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.calls++
	u.request, u.accountID = req, accountID
	u.body, _ = io.ReadAll(req.Body)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return u.response, u.err
}

func (u *generationTestUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func generationSSE(data any) string {
	encoded, _ := json.Marshal(data)
	return "data: " + string(encoded) + "\n\n"
}

func generationService(account *Account, upstream *generationTestUpstream) *AccountTestService {
	return &AccountTestService{
		accountRepo:  &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}},
		httpUpstream: upstream,
		cfg:          &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
}

func generationTestAccount(platform string) *Account {
	return &Account{ID: 42, Platform: platform, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-secret", "base_url": "https://generation.example"}}
}

func TestRunGenerationBackgroundGenerationSendsPromptAndPreservesHTML(t *testing.T) {
	// A response larger than a typical connectivity preview must survive intact,
	// including newlines, Unicode and content split across many SSE chunks.
	html := "<!DOCTYPE html>\n<html><body><svg>" + strings.Repeat("<!-- 鹈鹕 & bicycle -->\n", 600) + "</svg></body></html>"
	prompt := "\n  创建一个html，使用SVG绘制鹈鹕骑自行车\n"
	for _, tc := range []struct {
		name, platform, model, protocol, promptPath, budgetPath string
		responses                                               bool
	}{
		{"claude", PlatformAnthropic, "claude-sonnet-4-6", APIProtocolAnthropic, "messages.0.content.0.text", "max_tokens", false},
		{"responses", PlatformOpenAI, "gpt-4.1", APIProtocolResponses, "input.0.content.0.text", "max_output_tokens", true},
		{"chat", PlatformOpenAI, "gpt-4.1", APIProtocolChatCompletions, "messages.0.content", "max_tokens", false},
		{"chat reasoning", PlatformOpenAI, "gpt-6.1-sol", APIProtocolChatCompletions, "messages.0.content", "max_completion_tokens", false},
		{"chat namespaced reasoning", PlatformOpenAI, "openai/gpt-6-sol", APIProtocolChatCompletions, "messages.0.content", "max_completion_tokens", false},
		{"gemini", PlatformGemini, "gemini-2.5-flash", "gemini", "contents.0.parts.0.text", "generationConfig.maxOutputTokens", false},
		{"grok", PlatformGrok, "grok-4", APIProtocolResponses, "input.0.content.0.text", "max_output_tokens", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := generationTestAccount(tc.platform)
			account.Credentials["model_mapping"] = map[string]any{"my-model": tc.model}
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: tc.responses}
			var stream strings.Builder
			for _, part := range []string{html[:100], html[100:1000], html[1000:]} {
				switch tc.protocol {
				case APIProtocolAnthropic:
					stream.WriteString(generationSSE(map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": part}}))
				case APIProtocolResponses:
					stream.WriteString(generationSSE(map[string]any{"type": "response.output_text.delta", "delta": part}))
				case APIProtocolChatCompletions:
					stream.WriteString(generationSSE(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": part}}}}))
				case "gemini":
					stream.WriteString(generationSSE(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": part}}}}}}))
				}
			}
			switch tc.protocol {
			case APIProtocolAnthropic:
				stream.WriteString("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}")
			case APIProtocolResponses:
				stream.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}")
			case APIProtocolChatCompletions:
				stream.WriteString("data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\"}]}")
			case "gemini":
				stream.WriteString("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}")
			}
			upstream := &generationTestUpstream{response: newJSONResponse(http.StatusOK, stream.String())}
			result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "my-model", prompt)
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, html, result.ResponseText)
			require.Equal(t, prompt, gjson.GetBytes(upstream.body, tc.promptPath).String())
			require.EqualValues(t, accountTestGenerationMaxTokens, gjson.GetBytes(upstream.body, tc.budgetPath).Int())
			require.Equal(t, account.ID, upstream.accountID)
			require.Equal(t, 1, upstream.calls)
			if tc.protocol == "gemini" {
				require.Contains(t, upstream.request.URL.Path, tc.model)
			} else {
				require.Equal(t, tc.model, gjson.GetBytes(upstream.body, "model").String())
			}
		})
	}
}

func TestRunGenerationBackgroundGenerationFailuresRetainPartialContent(t *testing.T) {
	for _, tc := range []struct {
		name, platform, stream, expected, partial string
		responses                                 bool
	}{
		{"responses abrupt EOF", PlatformOpenAI, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html>\"}\n", "before completion", "<html>", true},
		{"responses incomplete", PlatformOpenAI, "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n", "max_output_tokens", "", true},
		{"responses failed done", PlatformOpenAI, "data: {\"type\":\"response.done\",\"response\":{\"status\":\"failed\"}}\n", "failed", "", true},
		{"responses error", PlatformOpenAI, "data: {\"type\":\"error\",\"message\":\"model unavailable\"}\n", "model unavailable", "", true},
		{"responses empty", PlatformOpenAI, "data: {\"type\":\"response.completed\"}\n", "empty response", "", true},
		{"claude truncated", PlatformAnthropic, "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"<html>\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n", "max_tokens", "<html>", false},
		{"claude abrupt EOF", PlatformAnthropic, "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"<html>\"}}\n", "before completion", "<html>", false},
		{"chat length", PlatformOpenAI, "data: {\"choices\":[{\"delta\":{\"content\":\"<html>\"},\"finish_reason\":\"length\"}]}\n", "length", "<html>", false},
		{"chat missing finish", PlatformOpenAI, "data: {\"choices\":[{\"delta\":{\"content\":\"<html>\"}}]}\n\ndata: [DONE]\n", "before completion", "<html>", false},
		{"gemini max tokens", PlatformGemini, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"<html>\"}]},\"finishReason\":\"MAX_TOKENS\"}]}\n", "MAX_TOKENS", "<html>", false},
		{"gemini blocked", PlatformGemini, "data: {\"promptFeedback\":{\"blockReason\":\"SAFETY\"}}\n", "SAFETY", "", false},
		{"gemini missing finish", PlatformGemini, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"<html>\"}]}}]}\n", "before completion", "<html>", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := generationTestAccount(tc.platform)
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: tc.responses}
			upstream := &generationTestUpstream{response: newJSONResponse(http.StatusOK, tc.stream)}
			result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "text-model", "Generate an HTML animation")
			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.Contains(t, result.ErrorMessage, tc.expected)
			require.Equal(t, tc.partial, result.ResponseText)
		})
	}
}

func TestRunGenerationBackgroundGenerationTransportAndCancellation(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, context.Canceled, io.ErrUnexpectedEOF} {
		t.Run(failure.Error(), func(t *testing.T) {
			account := generationTestAccount(PlatformAnthropic)
			upstream := &generationTestUpstream{err: failure}
			result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "claude-sonnet-4-6", "Generate HTML")
			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.Contains(t, result.ErrorMessage, failure.Error())
		})
	}
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		account := generationTestAccount(PlatformAnthropic)
		result, err := generationService(account, &generationTestUpstream{}).RunGenerationBackground(ctx, account.ID, "claude-sonnet-4-6", "Generate HTML")
		require.NoError(t, err)
		require.Equal(t, "failed", result.Status)
		require.Contains(t, result.ErrorMessage, "context canceled")
	})
	t.Run("HTTP error", func(t *testing.T) {
		account := generationTestAccount(PlatformOpenAI)
		upstream := &generationTestUpstream{response: newJSONResponse(http.StatusServiceUnavailable, `{"error":"temporarily unavailable"}`)}
		result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "gpt-4.1", "Generate HTML")
		require.NoError(t, err)
		require.Equal(t, "failed", result.Status)
		require.Contains(t, result.ErrorMessage, "503")
		require.Contains(t, result.ErrorMessage, "temporarily unavailable")
	})
}

func TestRunGenerationBackgroundGenerationRejectsMediaBeforeRequest(t *testing.T) {
	account := generationTestAccount(PlatformOpenAI)
	account.Credentials["model_mapping"] = map[string]any{"picture": "gpt-image-2"}
	upstream := &generationTestUpstream{}
	result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "picture", "Generate HTML")
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.ErrorMessage, "select a text model")
	require.Zero(t, upstream.calls)
}

func TestParseGenerationTestSSEOutputRequiresSuccessfulCompletion(t *testing.T) {
	text, errMsg := parseGenerationTestSSEOutput("data: {\"type\":\"content\",\"text\":\"partial\"}\n\n")
	require.Equal(t, "partial", text)
	require.Contains(t, errMsg, "without successful completion")
}

func TestRunGenerationBackgroundGenerationAntigravityOAuth(t *testing.T) {
	account := generationTestAccount(PlatformAntigravity)
	account.Type = AccountTypeOAuth
	account.Credentials["access_token"] = "test-oauth-token"
	account.Credentials["project_id"] = "test-project"
	account.Credentials["model_mapping"] = map[string]any{"my-model": "claude-sonnet-4-6"}
	upstream := &generationTestUpstream{response: newJSONResponse(http.StatusOK,
		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"<html><svg></svg></html>"}]},"finishReason":"STOP"}]}}`+"\n\n")}
	svc := generationService(account, upstream)
	svc.antigravityGatewayService = &AntigravityGatewayService{
		tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream, accountRepo: svc.accountRepo,
	}
	result, err := svc.RunGenerationBackground(context.Background(), account.ID, "my-model", "Custom pelican animation")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Equal(t, "<html><svg></svg></html>", result.ResponseText)
	require.Equal(t, "Custom pelican animation", gjson.GetBytes(upstream.body, "request.contents.0.parts.0.text").String())
	require.EqualValues(t, accountTestGenerationMaxTokens, gjson.GetBytes(upstream.body, "request.generationConfig.maxOutputTokens").Int())
	require.Equal(t, "claude-sonnet-4-6", gjson.GetBytes(upstream.body, "model").String())
	require.Equal(t, account.ID, upstream.accountID)
	require.Equal(t, "Bearer test-oauth-token", upstream.request.Header.Get("Authorization"))
}

func TestRunGenerationBackgroundGenerationAdaptiveUsesOneRequest(t *testing.T) {
	account := adaptiveCNAccountTestAccount(42, PlatformDeepseek)
	account.Credentials["api_base_urls"] = map[string]any{APIProtocolChatCompletions: "https://chat.example/v1"}
	account.Credentials["model_mapping"] = map[string]any{"my-model": "deepseek-chat"}
	upstream := &generationTestUpstream{response: newJSONResponse(http.StatusOK,
		`data: {"choices":[{"delta":{"content":"<html><svg></svg></html>"},"finish_reason":"stop"}]}`+"\n\n")}
	result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "my-model", "My custom prompt")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Equal(t, 1, upstream.calls)
	require.Equal(t, "https://chat.example/v1/chat/completions", upstream.request.URL.String())
	require.Equal(t, "My custom prompt", gjson.GetBytes(upstream.body, "messages.0.content").String())
	require.Equal(t, "deepseek-chat", gjson.GetBytes(upstream.body, "model").String())
}

func TestRunGenerationBackgroundGenerationOAuthOmitsUnsupportedBudget(t *testing.T) {
	account := generationTestAccount(PlatformOpenAI)
	account.Type = AccountTypeOAuth
	account.Credentials["access_token"] = "oauth-token"
	upstream := &generationTestUpstream{response: newJSONResponse(http.StatusOK,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"content":[{"type":"output_text","text":"<html><svg></svg></html>"}]}]}}`+"\n\n")}
	result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "gpt-5.4", "My custom prompt")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Equal(t, "<html><svg></svg></html>", result.ResponseText)
	require.Equal(t, "My custom prompt", gjson.GetBytes(upstream.body, "input.0.content.0.text").String())
	require.False(t, gjson.GetBytes(upstream.body, "max_output_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.body, "store").Bool())
}

func TestRunGenerationBackgroundGenerationBedrock(t *testing.T) {
	for _, reason := range []string{"end_turn", "max_tokens"} {
		t.Run(reason, func(t *testing.T) {
			account := generationTestAccount(PlatformAnthropic)
			account.Type = AccountTypeBedrock
			account.Credentials["auth_mode"] = "apikey"
			account.Credentials["aws_region"] = "us-east-1"
			upstream := &generationTestUpstream{response: newJSONResponse(http.StatusOK,
				`{"content":[{"type":"text","text":"<html>"},{"type":"text","text":"<svg></svg></html>"}],"stop_reason":"`+reason+`"}`)}
			result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "claude-sonnet-4-5", "My custom prompt")
			require.NoError(t, err)
			require.Equal(t, "<html><svg></svg></html>", result.ResponseText)
			if reason == "end_turn" {
				require.Equal(t, "success", result.Status, result.ErrorMessage)
			} else {
				require.Equal(t, "failed", result.Status)
				require.Contains(t, result.ErrorMessage, "max_tokens")
			}
			require.Equal(t, "My custom prompt", gjson.GetBytes(upstream.body, "messages.0.content.0.text").String())
			require.EqualValues(t, accountTestGenerationMaxTokens, gjson.GetBytes(upstream.body, "max_tokens").Int())
		})
	}
}

type generationReadFailure struct{}

func (generationReadFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRunGenerationBackgroundGenerationReadErrorRetainsPartialText(t *testing.T) {
	account := generationTestAccount(PlatformAnthropic)
	upstream := &generationTestUpstream{response: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(io.MultiReader(
		strings.NewReader("data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"<html>\"}}\n\n"), generationReadFailure{},
	))}}
	result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "claude-sonnet-4-6", "My custom prompt")
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "<html>", result.ResponseText)
	require.Contains(t, result.ErrorMessage, "unexpected EOF")
}
