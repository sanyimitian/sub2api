package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func identificationStream(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n\n"
}
func identificationChatStream(text string) string {
	return identificationStream(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": "stop"}}})
}
func TestModelIdentificationGenerationUsesConfiguredEndpointAndKey(t *testing.T) {
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAnthropic, "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, http.MethodPost, r.Method)
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Contains(t, string(body), DefaultDegradationTestPrompt)
				require.NotContains(t, string(body), "health-check")
				w.Header().Set("Content-Type", "text/event-stream")
				switch protocol {
				case APIProtocolChatCompletions:
					require.Equal(t, "/prefix/v1/chat/completions", r.URL.Path)
					require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
					require.Equal(t, "alias-model", gjson.GetBytes(body, "model").String())
					require.Equal(t, "high", gjson.GetBytes(body, "reasoning_effort").String())
					require.True(t, gjson.GetBytes(body, "stream").Bool())
					_, _ = io.WriteString(w, identificationChatStream("<svg>鹈鹕</svg>"))
				case APIProtocolResponses:
					require.Equal(t, "/prefix/v1/responses", r.URL.Path)
					require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
					require.Equal(t, "alias-model", gjson.GetBytes(body, "model").String())
					require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
					_, _ = io.WriteString(w, identificationStream(map[string]any{"type": "response.output_text.delta", "delta": "<svg>鹈鹕</svg>"})+identificationStream(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}}))
				case APIProtocolAnthropic:
					require.Equal(t, "/prefix/v1/messages", r.URL.Path)
					require.Equal(t, "test-key", r.Header.Get("x-api-key"))
					require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
					require.Empty(t, r.Header.Get("Authorization"))
					require.Equal(t, int64(8192), gjson.GetBytes(body, "max_tokens").Int())
					_, _ = io.WriteString(w, identificationStream(map[string]any{"type": "content_block_delta", "delta": map[string]any{"text": "<svg>鹈鹕</svg>"}})+identificationStream(map[string]any{"type": "message_stop"}))
				case "gemini":
					require.Equal(t, "/prefix/v1beta/models/alias-model:streamGenerateContent", r.URL.Path)
					require.Equal(t, "sse", r.URL.Query().Get("alt"))
					require.Empty(t, r.URL.Query().Get("key"))
					require.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
					require.Empty(t, r.Header.Get("Authorization"))
					_, _ = io.WriteString(w, identificationStream(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "<svg>鹈鹕</svg>"}}}, "finishReason": "STOP"}}}))
				}
			}))
			defer server.Close()
			endpoint := server.URL + "/prefix/v1"
			effort := "high"
			if protocol == "gemini" {
				endpoint = server.URL + "/prefix"
			}
			if protocol == "gemini" || protocol == APIProtocolAnthropic {
				effort = ""
			}
			repo := &identificationRepoStub{target: ModelIdentificationTarget{Endpoint: endpoint, EncryptedAPIKey: "encrypted:test-key", Protocol: protocol}}
			svc := NewGroupGenerationService(identificationEncryptor{})
			svc.client = server.Client()
			result, err := svc.RunGroupGeneration(context.Background(), &repo.target, "alias-model", DefaultDegradationTestPrompt, effort)
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, "<svg>鹈鹕</svg>", result.ResponseText)
			require.Equal(t, 1, calls)
		})
	}
}

type identificationTransport func(*http.Request) (*http.Response, error)

func (f identificationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestModelIdentificationGenerationFailuresAndSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"truncated", identificationStream(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "partial"}}}}), 200},
		{"key-echo", identificationChatStream("<svg>test-key</svg>"), 200},
		{"endpoint-echo", identificationChatStream("<svg>private.example.com</svg>"), 200},
		{"length", identificationStream(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "partial"}, "finish_reason": "length"}}}), 200},
		{"failure", identificationStream(map[string]any{"error": map[string]any{"message": "failed test-key private.example.com"}}), 200},
		{"http-error", "test-key private.example.com", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			repo := &identificationRepoStub{target: ModelIdentificationTarget{Endpoint: "https://private.example.com", EncryptedAPIKey: "encrypted:test-key", Protocol: APIProtocolChatCompletions}}
			svc := NewGroupGenerationService(identificationEncryptor{})
			svc.client = &http.Client{Transport: identificationTransport(func(r *http.Request) (*http.Response, error) {
				redirected, _ := http.NewRequestWithContext(r.Context(), r.Method, server.URL, r.Body)
				return server.Client().Do(redirected)
			})}
			result, err := svc.RunGroupGeneration(context.Background(), &repo.target, "alias", "prompt", "")
			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.NotContains(t, result.ResponseText+result.ErrorMessage, "test-key")
			require.NotContains(t, result.ResponseText+result.ErrorMessage, "private.example.com")
		})
	}
}
func TestModelIdentificationGenerationRejectsRedirectsAndCancels(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/leak", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	repo := &identificationRepoStub{target: ModelIdentificationTarget{Endpoint: server.URL, EncryptedAPIKey: "encrypted:test-key", Protocol: APIProtocolAnthropic}}
	svc := NewGroupGenerationService(identificationEncryptor{})
	svc.client.Transport = server.Client().Transport
	result, err := svc.RunGroupGeneration(context.Background(), &repo.target, "alias", "prompt", "")
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.ErrorMessage, "307")
	require.Equal(t, 1, calls)
	svc.client.Transport = identificationTransport(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = svc.RunGroupGeneration(ctx, &repo.target, "alias", "prompt", "")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
func TestModelIdentificationGenerationNeverSilentlyDropsEffort(t *testing.T) {
	repo := &identificationRepoStub{target: ModelIdentificationTarget{Endpoint: "https://example.com", EncryptedAPIKey: "encrypted:test-key", Protocol: APIProtocolAnthropic}}
	svc := NewGroupGenerationService(identificationEncryptor{})
	svc.client.Transport = identificationTransport(func(*http.Request) (*http.Response, error) { t.Fatal("must reject before sending"); return nil, nil })
	_, err := svc.RunGroupGeneration(context.Background(), &repo.target, "alias", "prompt", "high")
	require.ErrorContains(t, err, "默认推理强度")
	clean := identificationRedactor("https://private.example.com/prefix", "sensitive-key")
	require.NotContains(t, clean("sensitive-key https://private.example.com/prefix"), "sensitive")
	require.NotContains(t, clean("private.example.com"), "private")
	require.Equal(t, "<svg>鹈鹕</svg>", clean("<svg>鹈鹕</svg>"))
}

func TestModelIdentificationRunnerUsesCapturedConfiguration(t *testing.T) {
	targetID := int64(4)
	claimed := &DegradationTestPlan{GroupID: &targetID, ModelID: "old-model", Prompt: "old-prompt", ReasoningEffort: "low", IdentificationTarget: &ModelIdentificationTarget{ID: targetID, Endpoint: "https://old.example.com", EncryptedAPIKey: "encrypted:old-key", Protocol: APIProtocolChatCompletions}}
	// HTTP 返回副本不得与后台执行快照共用目标对象。
	accepted := cloneDegradationTestPlan(claimed)
	accepted.IdentificationTarget.Endpoint = "https://new.example.com"
	accepted.IdentificationTarget.EncryptedAPIKey = "encrypted:new-key"
	accepted.IdentificationTarget.Protocol = APIProtocolResponses
	accepted.ModelID = "new-model"
	generator := NewGroupGenerationService(identificationEncryptor{})
	generator.client = &http.Client{Transport: identificationTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https://old.example.com/v1/chat/completions", r.URL.String())
		require.Equal(t, "Bearer old-key", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "old-model", gjson.GetBytes(body, "model").String())
		require.Equal(t, "low", gjson.GetBytes(body, "reasoning_effort").String())
		require.Equal(t, "old-prompt", gjson.GetBytes(body, "messages.0.content").String())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(identificationChatStream("<svg>鹈鹕</svg>"))), Header: make(http.Header)}, nil
	})}
	runner := NewDegradationTestRunnerService(nil, nil, nil, nil)
	defer runner.Stop()
	runner.groupTestSvc = generator
	result, err := runner.invokeTest(context.Background(), claimed)
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	claimed.IdentificationTarget = nil
	_, err = runner.invokeTest(context.Background(), claimed)
	require.ErrorContains(t, err, "配置快照")
}
