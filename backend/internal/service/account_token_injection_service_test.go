//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func injectionEvent(payload string) string {
	return "data:" + payload + string([]byte{13, 10, 13, 10})
}

func TestTokenInjectionUsageAndCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		wantInput, wantCache    int
		wantError, wantTTFT     bool
	}{
		{name: "responses cache included in total", body: injectionEvent(
			`{"type":"response.output_text.delta","delta":"OK"}`) + injectionEvent(
			`{"type":"response.completed","response":{"model":"gpt-test","usage":{"input_tokens":22438,"input_tokens_details":{"cached_tokens":22415},"output_tokens":5}}}`), wantInput: 22438, wantCache: 22415, wantTTFT: true},
		{name: "chat usage after finish", body: injectionEvent(
			`{"choices":[{"delta":{"content":"OK"},"finish_reason":null}]}`) + injectionEvent(
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`) + injectionEvent(
			`{"choices":[],"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens":1}}`), wantInput: 100, wantCache: 80, wantTTFT: true},
		{name: "json response", contentType: "application/json", body: `{"status":"completed","usage":{"input_tokens":120,"output_tokens":1}}`, wantInput: 120},
		{name: "json chat", contentType: "application/json", body: `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":1}}`, wantInput: 30},
		{name: "no final usage", body: injectionEvent(`{"type":"response.completed","response":{}}`), wantError: true},
		{name: "provisional usage only", body: injectionEvent(`{"type":"response.created","response":{"usage":{"input_tokens":0}}}`) + injectionEvent(`{"type":"response.completed","response":{}}`), wantError: true},
		{name: "provisional chat usage only", body: injectionEvent(`{"choices":[{"delta":{"content":"OK"}}],"usage":{"prompt_tokens":0}}`) + injectionEvent(`{"choices":[{"finish_reason":"stop"}]}`), wantError: true},
		{name: "truncated stream", body: injectionEvent(`{"usage":{"input_tokens":100}}`), wantError: true},
		{name: "cached exceeds total", body: injectionEvent(`{"type":"response.completed","response":{"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":30}}}}`), wantError: true},
		{name: "fractional tokens", body: injectionEvent(`{"type":"response.completed","response":{"usage":{"input_tokens":10.5}}}`), wantError: true},
		{name: "negative tokens", body: injectionEvent(`{"type":"response.completed","response":{"usage":{"input_tokens":-10}}}`), wantError: true},
		{name: "incomplete", body: injectionEvent(`{"type":"response.incomplete","response":{"usage":{"input_tokens":100}}}`), wantError: true},
		{name: "chat length limit", body: injectionEvent(`{"choices":[{"finish_reason":"length"}],"usage":{"prompt_tokens":100}}`), wantError: true},
		{name: "upstream error", body: injectionEvent(`{"error":{"message":"secret-token"}}`), wantError: true},
		{name: "final line without delimiter", body: `data:{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":1}}}`, wantInput: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := newJSONResponse(http.StatusOK, tc.body)
			resp.Header.Set("Content-Type", tc.contentType)
			sample, err := readTokenInjectionSample(resp, time.Now())
			if tc.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret-token")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantInput, sample.InputTokens)
			require.Equal(t, tc.wantCache, sample.CacheRead)
			require.Equal(t, tc.wantTTFT, sample.FirstTokenMS != nil)
			require.True(t, json.Valid(sample.RawUsage))
		})
	}
}

func TestTokenInjectionRegression(t *testing.T) {
	makeResult := func() TokenInjectionResult {
		r := TokenInjectionResult{Warnings: []string{}}
		for _, n := range []int{16, 273, 1041, 4113, 4113} {
			r.Samples = append(r.Samples, TokenInjectionSample{LocalTokens: n, InputTokens: n + 22422, ResponseModel: "gpt-test"})
		}
		return r
	}
	r := makeResult()
	analyzeTokenInjection(&r)
	require.Equal(t, "stable", r.Status)
	require.Equal(t, 22422, *r.EstimateTokens)
	require.Equal(t, 1.0, r.Slope)
	require.Zero(t, r.MaxResidual)
	for _, kind := range []string{"slope", "repeat", "model", "negative", "missing"} {
		t.Run(kind, func(t *testing.T) {
			r := makeResult()
			switch kind {
			case "slope":
				for i := range r.Samples {
					r.Samples[i].InputTokens = r.Samples[i].LocalTokens*2 + 22422
				}
			case "repeat":
				r.Samples[4].InputTokens += 1000
			case "model":
				r.Samples[2].ResponseModel = "other-model"
			case "negative":
				for i := range r.Samples {
					r.Samples[i].InputTokens = r.Samples[i].LocalTokens - 100
				}
			case "missing":
				r.Samples = r.Samples[:3]
			}
			analyzeTokenInjection(&r)
			require.Equal(t, "inconclusive", r.Status)
			require.Nil(t, r.EstimateTokens)
		})
	}
}

type injectionUpstream struct {
	t            *testing.T
	requests     []*http.Request
	accountIDs   []int64
	status       int
	afterRequest func()
}

func (u *injectionUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxy, id, concurrency, nil)
}
func (u *injectionUpstream) DoWithTLS(req *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	u.accountIDs = append(u.accountIDs, id)
	if u.status != 0 {
		return newJSONResponse(u.status, "sensitive upstream response"), nil
	}
	var body map[string]json.RawMessage
	require.NoError(u.t, json.NewDecoder(req.Body).Decode(&body))
	var model string
	require.NoError(u.t, json.Unmarshal(body["model"], &model))
	require.Equal(u.t, "gpt-6-astra", model)
	var input []struct {
		Content string `json:"content"`
	}
	require.NoError(u.t, json.Unmarshal(body["input"], &input))
	require.NotContains(u.t, body, "instructions")
	require.NotContains(u.t, body, "tools")
	codec, err := openAIInputTokensCodecForModel("gpt-6-astra")
	require.NoError(u.t, err)
	n, err := codec.Count(input[0].Content)
	require.NoError(u.t, err)
	if u.afterRequest != nil {
		u.afterRequest()
	}
	return newJSONResponse(200, injectionEvent(fmt.Sprintf(`{"type":"response.completed","response":{"model":"gpt-6-astra","usage":{"input_tokens":%d,"input_tokens_details":{"cached_tokens":20000},"output_tokens":1}}}`, n+22422))), nil
}

func newInjectionTestService(t *testing.T) (*AccountTestService, *injectionUpstream) {
	a := &Account{ID: 377, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test-secret", "base_url": "https://example.com", "model_mapping": map[string]any{"alias": "gpt-6-astra"}}}
	u := &injectionUpstream{t: t}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{377: a}}
	return &AccountTestService{accountRepo: repo, httpUpstream: u, cfg: &config.Config{}}, u
}

func TestTokenInjectionPinnedAccountAndModel(t *testing.T) {
	svc, u := newInjectionTestService(t)
	c, rec := newTestContext()
	require.NoError(t, svc.TestTokenInjection(c, 377, "alias", "responses"))
	require.Len(t, u.requests, 5)
	for i, req := range u.requests {
		require.Equal(t, int64(377), u.accountIDs[i])
		require.Equal(t, "/v1/responses", req.URL.Path)
		require.Equal(t, "Bearer test-secret", req.Header.Get("Authorization"))
		_, ok := req.Context().Deadline()
		require.True(t, ok)
	}
	require.Contains(t, rec.Body.String(), `"estimate_tokens":22422`)
	require.Contains(t, rec.Body.String(), `"requested_model":"alias"`)
	require.NotContains(t, rec.Body.String(), "test-secret")
	_, locked := svc.tokenInjectionTests.Load(int64(377))
	require.False(t, locked)
}

func TestTokenInjectionStopsWithoutRetryOrRecovery(t *testing.T) {
	svc, u := newInjectionTestService(t)
	u.status = 429
	c, rec := newTestContext()
	require.Error(t, svc.TestTokenInjection(c, 377, "gpt-6-astra", "responses"))
	require.Len(t, u.requests, 1)
	require.Contains(t, rec.Body.String(), "HTTP 429")
	require.NotContains(t, rec.Body.String(), "sensitive upstream response")
	require.NotContains(t, rec.Body.String(), "injection_complete")
}

func TestTokenInjectionCancellationAndConcurrentGuard(t *testing.T) {
	svc, u := newInjectionTestService(t)
	c, _ := newTestContext()
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	u.afterRequest = cancel
	require.Error(t, svc.TestTokenInjection(c, 377, "gpt-6-astra", "responses"))
	require.Len(t, u.requests, 1)
	c, rec := newTestContext()
	svc.tokenInjectionTests.Store(int64(377), struct{}{})
	require.Error(t, svc.TestTokenInjection(c, 377, "gpt-6-astra", "responses"))
	require.Contains(t, rec.Body.String(), "already_running")
	require.Len(t, u.requests, 1)
}

func TestTokenInjectionChatAndOAuthTargets(t *testing.T) {
	svc, _ := newInjectionTestService(t)
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "key"}, Extra: map[string]any{"openai_responses_supported": false}}
	target, err := svc.prepareTokenInjectionTarget(context.Background(), a, "gpt-4o", "auto")
	require.NoError(t, err)
	require.Equal(t, "chat", target.protocol)
	require.True(t, strings.HasSuffix(target.url, "/v1/chat/completions"))
	a.Type = AccountTypeOAuth
	a.Credentials = map[string]any{"access_token": "token"}
	_, err = svc.prepareTokenInjectionTarget(context.Background(), a, "gpt-4o", "chat")
	require.Error(t, err)
	target, err = svc.prepareTokenInjectionTarget(context.Background(), a, "gpt-4o", "auto")
	require.NoError(t, err)
	require.True(t, target.oauth)
	_, err = svc.prepareTokenInjectionTarget(context.Background(), a, "gpt-image-2", "responses")
	require.Error(t, err)
}

func TestTokenInjectionProtocolPayloads(t *testing.T) {
	for _, protocol := range []string{"chat", "oauth"} {
		t.Run(protocol, func(t *testing.T) {
			svc, _ := newInjectionTestService(t)
			a := &Account{ID: 377, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}}
			body := injectionEvent(`{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":1}}`)
			selectedProtocol := protocol
			if protocol == "oauth" {
				a.Type = AccountTypeOAuth
				a.Credentials = map[string]any{"access_token": "test-access-token", "chatgpt_account_id": "test-chatgpt-account"}
				body = injectionEvent(`{"type":"response.completed","response":{"usage":{"input_tokens":30,"output_tokens":1}}}`)
				selectedProtocol = "responses"
			}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK, body)}}
			svc.httpUpstream = upstream
			target, err := svc.prepareTokenInjectionTarget(context.Background(), a, "gpt-6-astra", selectedProtocol)
			require.NoError(t, err)
			_, err = svc.runTokenInjectionSample(context.Background(), target, "tiny diagnostic input")
			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			request := upstream.requests[0]
			var payload map[string]any
			require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
			require.NotContains(t, payload, "tools")
			require.Equal(t, true, payload["stream"])
			if protocol == "chat" {
				require.Equal(t, "/v1/chat/completions", request.URL.Path)
				require.NotContains(t, payload, "instructions")
				require.NotContains(t, payload, "input")
				require.Equal(t, map[string]any{"include_usage": true}, payload["stream_options"])
				require.Equal(t, float64(128), payload["max_completion_tokens"])
				require.Equal(t, "tiny diagnostic input", payload["messages"].([]any)[0].(map[string]any)["content"])
			} else {
				require.Equal(t, "chatgpt.com", request.Host)
				require.Equal(t, "Bearer test-access-token", request.Header.Get("Authorization"))
				require.Equal(t, tokenInjectionInstructions, payload["instructions"])
				require.NotContains(t, payload, "max_output_tokens")
				require.Equal(t, false, payload["store"])
			}
		})
	}
}

func TestTokenInjectionCredentialShadowKeepsSelectedAccount(t *testing.T) {
	svc, _ := newInjectionTestService(t)
	parentID := int64(90)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "parent-token"}}
	shadow := &Account{ID: 91, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	svc.accountRepo = &mockAccountRepoForGemini{accountsByID: map[int64]*Account{parentID: parent}}
	target, err := svc.prepareTokenInjectionTarget(context.Background(), shadow, "gpt-6-astra", "auto")
	require.NoError(t, err)
	require.Same(t, shadow, target.account)
	require.Same(t, parent, target.credential)
	require.Equal(t, "parent-token", target.token)
}

func TestTokenInjectionRejectsOversizedUpstreamResponse(t *testing.T) {
	resp := newJSONResponse(http.StatusOK, "data:"+strings.Repeat("x", 1<<20))
	_, err := readTokenInjectionSample(resp, time.Now())
	require.Error(t, err)
	resp = newJSONResponse(http.StatusOK, "{"+strings.Repeat(" ", 2<<20)+"}")
	_, err = readTokenInjectionSample(resp, time.Now())
	require.Error(t, err)
}
