package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIWSLatencyMemoryCache struct {
	stubGatewayCache
	mu          sync.Mutex
	states      map[string][]byte
	probeStarts int
}

func (cache *openAIWSLatencyMemoryCache) GetOpenAIWSLatencyState(_ context.Context, key string) ([]byte, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return bytes.Clone(cache.states[key]), nil
}

func (cache *openAIWSLatencyMemoryCache) CompareAndSwapOpenAIWSLatencyState(_ context.Context, key string, expected, next []byte, _ time.Duration) (bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !bytes.Equal(cache.states[key], expected) {
		return false, nil
	}
	var oldState, newState openAIWSLatencyState
	_ = json.Unmarshal(expected, &oldState)
	_ = json.Unmarshal(next, &newState)
	if !strings.HasPrefix(key, "candidate_pool:") && oldState.ProbeID == "" && newState.ProbeID != "" {
		cache.probeStarts++
	}
	cache.states[key] = bytes.Clone(next)
	return true, nil
}

func newOpenAIWSLatencyTestService(t *testing.T) (*OpenAIGatewayService, *openAIWSLatencyMemoryCache) {
	t.Helper()
	gatewayForwardingSF.Forget("gateway_forwarding")
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, Consecutive: 3, SlowMS: 15000}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	t.Cleanup(func() { gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{}) })
	cache := &openAIWSLatencyMemoryCache{states: make(map[string][]byte)}
	cfg := newSchedulerTestOpenAIWSV2Config()
	svc := &OpenAIGatewayService{cfg: cfg, cache: cache, settingService: NewSettingService(&openAIAdvancedSchedulerSettingRepoStub{}, cfg), openaiWSStateStore: NewOpenAIWSStateStore(nil), accountRepo: schedulerTestOpenAIAccountRepo{}}
	return svc, cache
}

func newOpenAIWSLatencyTestSession(t *testing.T, svc *OpenAIGatewayService, thread string, apiKeyID int64, payload []byte) *OpenAIWSLatencySession {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set("thread-id", thread)
	groupID := int64(1)
	session := svc.NewOpenAIWSLatencySession(context.Background(), c, payload, &groupID, &APIKey{ID: apiKeyID}, "test-session", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
	require.NotNil(t, session)
	return session
}

func seedOpenAIWSLatencyPending(t *testing.T, session *OpenAIWSLatencySession) {
	t.Helper()
	_, updated := session.service.updateOpenAIWSLatencyState(context.Background(), session.key(session.model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, TargetID: 22, PendingUntil: time.Now().Add(time.Minute).UnixMilli()}
		return true
	})
	require.True(t, updated)
}

func TestOpenAIWSLatencyCountsOnlyConsecutiveUserRequests(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	user := []byte(`{"type":"response.create","input":[{"role":"user","content":"hello"}]}`)
	session := newOpenAIWSLatencyTestSession(t, svc, "count", 1, user)
	account := &Account{ID: 11}
	slowMS, fastMS := 16000, 100
	slow, fast := &OpenAIForwardResult{FirstTokenMs: &slowMS}, &OpenAIForwardResult{FirstTokenMs: &fastMS}
	session.Observe(context.Background(), 1, account, slow, nil)
	require.Equal(t, 1, session.state(context.Background()).SlowCount)
	tool := []byte(`{"input":[{"type":"function_call_output","call_id":"call_1","output":"done"}]}`)
	require.NoError(t, session.BeforeRequest(context.Background(), 2, account, tool, ""))
	session.Observe(context.Background(), 2, account, slow, nil)
	require.Equal(t, 1, session.state(context.Background()).SlowCount)
	require.NoError(t, session.BeforeRequest(context.Background(), 3, account, user, ""))
	session.Observe(context.Background(), 3, account, fast, nil)
	require.Zero(t, session.state(context.Background()).SlowCount)
	for turn := 4; turn <= 6; turn++ {
		require.NoError(t, session.BeforeRequest(context.Background(), turn, account, user, ""))
		session.Observe(context.Background(), turn, account, slow, nil)
	}
	cache.mu.Lock()
	starts := cache.probeStarts
	cache.mu.Unlock()
	require.Equal(t, 1, starts)
	require.Eventually(t, func() bool { return session.state(context.Background()).ProbeID == "" }, time.Second, time.Millisecond)
	require.Greater(t, session.state(context.Background()).CooldownUntil, time.Now().UnixMilli())
}

func TestOpenAIWSLatencyMinTPSThresholdRequiresBothConditions(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
		latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, Consecutive: 3, SlowMS: 15000, MinTPS: 20},
		expiresAt:     time.Now().Add(time.Hour).UnixNano(),
	})
	user := []byte(`{"type":"response.create","input":[{"role":"user","content":"hello"}]}`)
	session := newOpenAIWSLatencyTestSession(t, svc, "min-tps", 1, user)
	account := &Account{ID: 11}
	slowMS := 16000
	base := OpenAIForwardResult{FirstTokenMs: &slowMS, Duration: time.Second}

	fastOutput := base
	fastOutput.Usage.OutputTokens = 30
	require.NoError(t, session.BeforeRequest(context.Background(), 1, account, user, ""))
	session.Observe(context.Background(), 1, account, &fastOutput, nil)
	require.Zero(t, session.state(context.Background()).SlowCount)

	slowOutput := base
	slowOutput.Usage.OutputTokens = 10
	require.NoError(t, session.BeforeRequest(context.Background(), 2, account, user, ""))
	session.Observe(context.Background(), 2, account, &slowOutput, nil)
	require.Equal(t, 1, session.state(context.Background()).SlowCount)

	missingOutput := base
	require.NoError(t, session.BeforeRequest(context.Background(), 3, account, user, ""))
	session.Observe(context.Background(), 3, account, &missingOutput, nil)
	require.Zero(t, session.state(context.Background()).SlowCount)
}

func TestOpenAIWSOutputTPSMatchesUsageRateDefinition(t *testing.T) {
	result := &OpenAIForwardResult{Duration: 2 * time.Second, Usage: OpenAIUsage{OutputTokens: 100}}
	tps, ok := openAIWSOutputTPS(result)
	require.True(t, ok)
	require.Equal(t, float64(50), tps)

	result.ImageCount = 1
	_, ok = openAIWSOutputTPS(result)
	require.False(t, ok)
}

func TestOpenAIWSLatencyReconnectPreservesPendingAcrossInstances(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	payload := []byte(`{"input":[{"role":"user","content":"question"}]}`)
	old := newOpenAIWSLatencyTestSession(t, svc, "reconnect", 1, payload)
	seedOpenAIWSLatencyPending(t, old)
	err := old.BeforeRequest(context.Background(), 2, &Account{ID: 11}, payload, "")
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, OpenAIWSLatencySwitchCloseCode, closeErr.StatusCode())
	require.Equal(t, "Request terminated", closeErr.Reason())
	require.NotEmpty(t, old.state(context.Background()).ReconnectFrom)
	otherService := &OpenAIGatewayService{cfg: svc.cfg, cache: svc.cache, settingService: svc.settingService}
	newSession := newOpenAIWSLatencyTestSession(t, otherService, "reconnect", 1, payload)
	invalid := []byte(`{"previous_response_id":"resp_A","input":[{"role":"user","content":"question"}]}`)
	require.ErrorAs(t, newSession.Prepare(context.Background(), invalid), &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode(), "a delta-only retry must not reach the new upstream")
	complete := []byte(`{"input":[{"type":"compaction","encrypted_content":"compacted-window"},{"role":"user","content":"question"}]}`)
	copyBefore := bytes.Clone(complete)
	require.NoError(t, newSession.Prepare(context.Background(), complete))
	require.True(t, newSession.recovering)
	require.Equal(t, copyBefore, complete, "compacted effective input must remain intact")
	newSession.Observe(context.Background(), 1, &Account{ID: 22}, &OpenAIForwardResult{}, nil)
	require.Zero(t, newSession.state(context.Background()).TargetID)
	require.Equal(t, int64(22), newSession.state(context.Background()).AccountID)
}

func TestOpenAIHTTPResponsesLatencyRetrySwitchesOnlyWithCompleteContext(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	groupID := int64(1)
	apiKey := &APIKey{ID: 7, UserID: 42}
	payload := []byte(`{"model":"gpt-5.1","stream":true,"input":[{"role":"user","content":"complete question"}]}`)
	newSession := func(body []byte) *OpenAIWSLatencySession {
		return svc.NewOpenAIHTTPResponsesLatencySession(context.Background(), &groupID, apiKey, "http-session", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, body)
	}
	session := newSession(payload)
	require.NotNil(t, session)
	require.Equal(t, OpenAIUpstreamTransportHTTPSSE, session.requiredTransport)
	seedOpenAIWSLatencyPending(t, session)

	shouldRetry, err := session.PrepareHTTP(context.Background(), payload)
	require.NoError(t, err)
	require.True(t, shouldRetry, "the first request after a completed probe must be stopped before upstream forwarding")
	require.NotEmpty(t, session.state(context.Background()).ReconnectFrom)

	retrySession := newSession(payload)
	shouldRetry, err = retrySession.PrepareHTTP(context.Background(), payload)
	require.NoError(t, err)
	require.False(t, shouldRetry)
	require.True(t, retrySession.recovering)
	require.True(t, retrySession.recoveryConfirmed)

	account := Account{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "sk-test"}}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{account}}
	excluded := make(map[int64]struct{})
	selection, decision, err := retrySession.SelectTarget(context.Background(), excluded, OpenAIUpstreamTransportHTTPSSE)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(22), selection.Account.ID)
	require.Equal(t, "latency_switch", decision.Layer)
	require.Contains(t, excluded, int64(11))
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	_, updated := svc.updateOpenAIWSLatencyState(context.Background(), session.key(session.model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, TargetID: 22, PendingUntil: time.Now().Add(time.Minute).UnixMilli()}
		return true
	})
	require.True(t, updated)
	first, err := newSession(payload).PrepareHTTP(context.Background(), payload)
	require.NoError(t, err)
	require.True(t, first)
	invalidRetry := []byte(`{"model":"gpt-5.1","stream":true,"previous_response_id":"resp_old","input":[{"role":"user","content":"follow up"}]}`)
	_, err = newSession(invalidRetry).PrepareHTTP(context.Background(), invalidRetry)
	require.ErrorContains(t, err, "without previous_response_id")
}

func TestOpenAIHTTPResponsesLatencyCountsSlowStreamRequests(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	groupID := int64(1)
	apiKey := &APIKey{ID: 8, UserID: 43}
	payload := []byte(`{"model":"gpt-5.1","stream":true,"input":[{"role":"user","content":"question"}]}`)
	account := &Account{ID: 11}
	slowMS := 16000
	var lastSession *OpenAIWSLatencySession
	for i := 0; i < 3; i++ {
		lastSession = svc.NewOpenAIHTTPResponsesLatencySession(context.Background(), &groupID, apiKey, "same-http-session", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload)
		require.NotNil(t, lastSession)
		lastSession.Observe(context.Background(), 1, account, &OpenAIForwardResult{FirstTokenMs: &slowMS}, nil)
	}
	cache.mu.Lock()
	starts := cache.probeStarts
	cache.mu.Unlock()
	require.Equal(t, 1, starts, "the third consecutive slow HTTP SSE response starts one configured probe batch")
	require.Eventually(t, func() bool { return lastSession.state(context.Background()).ProbeID == "" }, time.Second, time.Millisecond)
}

func TestOpenAIWSLatencyScopeIsolationExpiryAndDisable(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	payload := []byte(`{"input":[{"role":"user","content":"hello"}]}`)
	session := newOpenAIWSLatencyTestSession(t, svc, "one", 1, payload)
	seedOpenAIWSLatencyPending(t, session)
	for _, other := range []*OpenAIWSLatencySession{newOpenAIWSLatencyTestSession(t, svc, "two", 1, payload), newOpenAIWSLatencyTestSession(t, svc, "one", 2, payload)} {
		require.NoError(t, other.BeforeRequest(context.Background(), 2, &Account{ID: 11}, payload, ""))
	}
	_, updated := svc.updateOpenAIWSLatencyState(context.Background(), session.key(session.model), func(state *openAIWSLatencyState) bool {
		state.PendingUntil = time.Now().Add(-time.Minute).UnixMilli()
		return true
	})
	require.True(t, updated)
	require.NoError(t, session.BeforeRequest(context.Background(), 2, &Account{ID: 11}, payload, ""))
	seedOpenAIWSLatencyPending(t, session)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{expiresAt: time.Now().Add(time.Hour).UnixNano()})
	require.NoError(t, session.BeforeRequest(context.Background(), 3, &Account{ID: 11}, payload, ""))
	require.NoError(t, session.Prepare(context.Background(), []byte(`{"previous_response_id":"resp_A"}`)))
}

func TestOpenAIWSLatencyToolAndPrewarmRecovery(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	warmup := []byte(`{"generate":false,"input":[]}`)
	session := newOpenAIWSLatencyTestSession(t, svc, "warmup", 1, warmup)
	seedOpenAIWSLatencyPending(t, session)
	require.NoError(t, session.Prepare(context.Background(), warmup))
	session.Observe(context.Background(), 1, &Account{ID: 22}, &OpenAIForwardResult{}, nil)
	require.Equal(t, int64(22), session.state(context.Background()).TargetID, "warmup does not prove recovery of user context")
	err := session.BeforeRequest(context.Background(), 2, &Account{ID: 22}, []byte(`{"previous_response_id":"resp_A","input":[{"role":"user","content":"next"}]}`), "")
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
	missingTool := []byte(`{"input":[{"type":"function_call_output","call_id":"missing","output":"done"}]}`)
	require.ErrorAs(t, session.BeforeRequest(context.Background(), 2, &Account{ID: 22}, missingTool, ""), &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode(), "tool deltas after target prewarm must also prove context recovery")
	require.NoError(t, session.Prepare(context.Background(), missingTool))
	require.False(t, openAIWSLatencyIsUserRequest(missingTool))
}

func TestOpenAIWSLatencyTargetRequiresOnlyCandidateUnderTenSeconds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []openAIWSLatencyProbeResult
		want    int64
	}{
		{"under ten seconds without relative improvement", []openAIWSLatencyProbeResult{{accountID: 1, ms: 5000, ok: true}, {accountID: 2, ms: 4900, ok: true}}, 2},
		{"candidate at ten seconds", []openAIWSLatencyProbeResult{{accountID: 2, ms: 10_000, ok: true}}, 0},
		{"candidate over ten seconds", []openAIWSLatencyProbeResult{{accountID: 2, ms: 10_001, ok: true}}, 0},
		{"current route is never a target", []openAIWSLatencyProbeResult{{accountID: 1, ms: 100, ok: true}}, 0},
		{"failed candidate", []openAIWSLatencyProbeResult{{accountID: 2, ms: 100, ok: false}}, 0},
		{"fastest qualifying candidate", []openAIWSLatencyProbeResult{{accountID: 2, ms: 9000, ok: true}, {accountID: 3, ms: 1000, ok: true}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, chooseOpenAIWSLatencyTarget(tc.results, 1)) })
	}
}

func TestOpenAIWSLatencyFastCandidateCanBeSelectedImmediately(t *testing.T) {
	require.True(t, isOpenAIWSLatencyFastCandidate(openAIWSLatencyProbeResult{accountID: 2, ms: 4_999, ok: true}, 1))
	require.False(t, isOpenAIWSLatencyFastCandidate(openAIWSLatencyProbeResult{accountID: 2, ms: 5_000, ok: true}, 1))
	require.False(t, isOpenAIWSLatencyFastCandidate(openAIWSLatencyProbeResult{accountID: 1, ms: 1_000, ok: true}, 1))
	require.False(t, isOpenAIWSLatencyFastCandidate(openAIWSLatencyProbeResult{accountID: 2, ms: 1_000}, 1))
}

func TestOpenAIWSLatencySelectTargetRechecksEligibilityAndExcludesOldRoute(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	payload := []byte(`{"input":[{"role":"user","content":"complete context"}]}`)
	session := newOpenAIWSLatencyTestSession(t, svc, "select", 1, payload)
	seedOpenAIWSLatencyPending(t, session)
	account := Account{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{1}, Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{account}}
	require.NoError(t, session.Prepare(context.Background(), payload))
	excluded := make(map[int64]struct{})
	selection, decision, err := session.SelectTarget(context.Background(), excluded, OpenAIUpstreamTransportResponsesWebsocketV2Ingress)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(22), selection.Account.ID)
	require.Equal(t, "latency_switch", decision.Layer)
	require.Contains(t, excluded, int64(11))
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	account.Schedulable = false
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{account}}
	selection, _, err = session.SelectTarget(context.Background(), excluded, OpenAIUpstreamTransportResponsesWebsocketV2Ingress)
	require.NoError(t, err)
	require.Nil(t, selection, "a probe result must not bypass a later account disable")
	account.Schedulable = true
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{account}}
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{22: false}})
	selection, _, err = session.SelectTarget(context.Background(), excluded, OpenAIUpstreamTransportResponsesWebsocketV2Ingress)
	require.NoError(t, err)
	require.Nil(t, selection, "a busy probe target should allow another eligible route")
	require.Contains(t, excluded, int64(22))
}

func TestOpenAIWSLatencyProbeUsesVisibleOutputAndRejectsFailedStream(t *testing.T) {
	writer := &openAIWSLatencyProbeWriter{header: make(http.Header), mode: OpenAITTFTModeVisible, started: time.Now().Add(-time.Second), firstMS: -1}
	_, err := writer.Write([]byte("data: {\"type\":\"response.created\"}\n\n"))
	require.NoError(t, err)
	require.Equal(t, -1, writer.firstMS)
	_, err = writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"O"))
	require.NoError(t, err)
	require.Equal(t, -1, writer.firstMS, "incomplete SSE lines must not start TTFT")
	_, err = writer.Write([]byte("K\"}\n\n"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, writer.firstMS, 1000)
	_, err = writer.Write([]byte("data: {\"type\":\"response.failed\"}\n\n"))
	require.NoError(t, err)
	require.True(t, writer.failed)
}

func TestOpenAIWSLatencyProbeForwardsSyntheticRequestOverWebSocket(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal string
		wantOK   bool
	}{
		{"completed", `{"type":"response.completed","response":{"id":"resp_probe","model":"gpt-5.1","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`, true},
		{"failed_after_delta", `{"type":"response.failed","response":{"id":"resp_probe","status":"failed","error":{"code":"server_error","message":"probe failed"}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			svc.cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			svc.cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			svc.cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			svc.cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			svc.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			svc.cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			upstream := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.output_text.delta","delta":"OK"}`),
				[]byte(tc.terminal),
			}}
			pool := newOpenAIWSConnPool(svc.cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: upstream})
			defer pool.Close()
			svc.openaiWSPool, svc.openaiWSResolver, svc.toolCorrector = pool, NewOpenAIWSProtocolResolver(svc.cfg), NewCodexToolCorrector()
			account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := svc.probeOpenAIWSLatencyAccount(ctx, account, "gpt-5.1")
			require.Equal(t, tc.wantOK, result.ok)
			require.False(t, result.timedOut)
			require.Len(t, upstream.writes, 1)
			require.Equal(t, false, upstream.lastWrite["store"])
			require.NotContains(t, upstream.lastWrite, "previous_response_id")
			require.NotContains(t, upstream.lastWrite, "tools")
			input, err := json.Marshal(upstream.lastWrite["input"])
			require.NoError(t, err)
			require.JSONEq(t, `[{"role":"user","content":"Reply with the single word OK."}]`, string(input))
		})
	}
}

type openAIWSLatencyHTTPProbeUpstream struct {
	HTTPUpstream
	request *http.Request
	body    []byte
}

func (upstream *openAIWSLatencyHTTPProbeUpstream) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	upstream.request = request.Clone(request.Context())
	upstream.body, _ = io.ReadAll(request.Body)
	_ = request.Body.Close()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\",\"model\":\"gpt-5.1\",\"status\":\"completed\"}}\n\n",
		)),
		Request: request,
	}, nil
}

func TestOpenAIWSLatencyProbeForwardsHTTPRequestsOverSSE(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.httpUpstream = &openAIWSLatencyHTTPProbeUpstream{}
	account := &Account{
		ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
		Extra: map[string]any{"responses_websockets_v2_enabled": true},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := svc.forwardOpenAIWSLatencyProbe(ctx, account, "gpt-5.1", OpenAIUpstreamTransportHTTPSSE)
	require.NoError(t, err)
	require.True(t, result.ok)
	require.False(t, result.timedOut)
	require.NotNil(t, svc.httpUpstream.(*openAIWSLatencyHTTPProbeUpstream).request)
	require.Equal(t, "/v1/responses", svc.httpUpstream.(*openAIWSLatencyHTTPProbeUpstream).request.URL.Path)
	require.True(t, gjson.GetBytes(svc.httpUpstream.(*openAIWSLatencyHTTPProbeUpstream).body, "stream").Bool())
	require.False(t, gjson.GetBytes(svc.httpUpstream.(*openAIWSLatencyHTTPProbeUpstream).body, "store").Bool())
	require.False(t, gjson.GetBytes(svc.httpUpstream.(*openAIWSLatencyHTTPProbeUpstream).body, "previous_response_id").Exists())
}

func TestOpenAIWSLatencyProbeBypassesNormalAccountSlots(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc.cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 20
	svc.cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 20
	svc.cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	svc.cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	svc.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	svc.cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	groupID := int64(1)
	account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*account}}
	var acquiredIDs []int64
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{11: false},
		acquiredIDs:    &acquiredIDs,
	})
	pool := newOpenAIWSConnPool(svc.cfg)
	dialer := &openAIWSLatencyProbeTestDialer{}
	pool.setClientDialerForTest(dialer)
	defer pool.Close()
	svc.openaiWSPool, svc.openaiWSResolver, svc.toolCorrector = pool, NewOpenAIWSProtocolResolver(svc.cfg), NewCodexToolCorrector()
	request := OpenAIAccountScheduleRequest{
		GroupID:            &groupID,
		Platform:           PlatformOpenAI,
		RequestedModel:     "gpt-5.1",
		RequiredTransport:  OpenAIUpstreamTransportResponsesWebsocketV2Ingress,
		RequiredCapability: OpenAIEndpointCapabilityResponses,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := svc.probeOpenAIWSLatencyCandidates(ctx, []*Account{account}, 0, "gpt-5.1", request)
	require.True(t, result.ok)
	require.Equal(t, int64(11), result.requestedAccountID)
	require.Equal(t, int64(11), result.accountID)
	require.Equal(t, []int64{11}, result.attemptedAccountIDs)
	require.Empty(t, acquiredIDs, "latency probes do not reserve ordinary request slots")
	dialer.mu.Lock()
	requests := dialer.requests
	dialer.mu.Unlock()
	require.Equal(t, 1, requests)
}

type openAIWSLatencyProbeTestDialer struct {
	mu              sync.Mutex
	calls           int
	requests        int
	firstTokenDelay func(http.Header) time.Duration
}

func (dialer *openAIWSLatencyProbeTestDialer) Dial(_ context.Context, _ string, headers http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	dialer.mu.Lock()
	dialer.calls++
	dialer.mu.Unlock()
	delay := 5 * time.Millisecond
	if dialer.firstTokenDelay != nil {
		delay = dialer.firstTokenDelay(headers)
	}
	return &openAIWSLatencyProbeCountingConn{
		openAIWSCaptureConn: &openAIWSCaptureConn{
			readDelays: []time.Duration{delay, 5 * time.Millisecond},
			events: [][]byte{
				[]byte(`{"type":"response.output_text.delta","delta":"OK"}`),
				[]byte(`{"type":"response.completed","response":{"id":"resp_probe","model":"gpt-5.1","status":"completed"}}`),
			},
		},
		dialer: dialer,
	}, 0, nil, nil
}

type openAIWSLatencyProbeCountingConn struct {
	*openAIWSCaptureConn
	dialer *openAIWSLatencyProbeTestDialer
}

func (conn *openAIWSLatencyProbeCountingConn) countRequest() {
	conn.dialer.mu.Lock()
	conn.dialer.requests++
	conn.dialer.mu.Unlock()
}

func (conn *openAIWSLatencyProbeCountingConn) WriteJSON(ctx context.Context, value any) error {
	conn.countRequest()
	return conn.openAIWSCaptureConn.WriteJSON(ctx, value)
}

func (conn *openAIWSLatencyProbeCountingConn) WriteFrame(ctx context.Context, messageType coderws.MessageType, payload []byte) error {
	conn.countRequest()
	return conn.openAIWSCaptureConn.WriteFrame(ctx, messageType, payload)
}

func TestOpenAIWSLatencyProbeCountChangesBetweenBatches(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc.cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 20
	svc.cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 20
	svc.cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	svc.cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	svc.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	svc.cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	pool := newOpenAIWSConnPool(svc.cfg)
	dialer := &openAIWSLatencyProbeTestDialer{}
	pool.setClientDialerForTest(dialer)
	defer pool.Close()
	svc.openaiWSPool, svc.openaiWSResolver, svc.toolCorrector = pool, NewOpenAIWSProtocolResolver(svc.cfg), NewCodexToolCorrector()
	groupID := int64(1)
	total := 0
	for batch, count := range []int{2, 8, 3, 20, 0} {
		// Keep the same service and limiter, with fresh accounts for each batch.
		accounts := make([]Account, 9)
		for i := range accounts {
			accounts[i] = Account{ID: int64(100*batch + i + 1), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
		}
		svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts}
		gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, ProbeCount: count}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		model := fmt.Sprintf("gpt-5.1-%d", batch)
		stateKey := fmt.Sprintf("probe-batch-%d", batch)
		_, updated := svc.updateOpenAIWSLatencyState(ctx, stateKey, func(state *openAIWSLatencyState) bool {
			*state = openAIWSLatencyState{AccountID: accounts[0].ID, ProbeID: "test-probe", ProbeUntil: time.Now().Add(time.Minute).UnixMilli()}
			return true
		})
		require.True(t, updated)
		svc.probeOpenAIWSLatencyRoutes(ctx, stateKey, &groupID, accounts[0].ID, model, model, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, "test-probe", openAIWSLatencyActor{})
		cancel()
		want := count
		if want == 0 {
			want = 6
		}
		total += want
		dialer.mu.Lock()
		requests := dialer.requests
		dialer.mu.Unlock()
		require.Equal(t, total, requests, "batch %d with configured probe count %d", batch, count)
	}
}

func TestOpenAIWSLatencySharesInFlightProbeAndReservesCandidatesOnce(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	ctx := context.Background()
	groupID := int64(1)
	poolKey := openAIWSLatencyCandidatePoolKey(&groupID, "gpt-5.1", OpenAIUpstreamTransportResponsesWebsocketV2Ingress)
	probeWindow := time.Minute

	require.True(t, svc.claimOpenAIWSLatencyProbe(ctx, poolKey, 11, "first-probe", probeWindow))
	require.False(t, svc.claimOpenAIWSLatencyProbe(ctx, poolKey, 11, "second-probe", probeWindow), "a concurrent trigger must join the active probe batch")

	results := []openAIWSLatencyProbeResult{
		{accountID: 22, ms: 1200, ok: true},
		{accountID: 33, ms: 2400, ok: true},
	}
	_, _ = svc.addOpenAIWSLatencyCandidates(ctx, poolKey, results, 600, 0, "", 300)
	pool := svc.getOpenAIWSLatencyCandidatePool(ctx, poolKey)
	require.Equal(t, "first-probe", pool.ProbeID, "joined requests wait for the same probe batch to publish its results")
	require.Len(t, pool.Candidates, 2)
	require.Greater(t, pool.Candidates[0].ExpiresAt, time.Now().UnixMilli())
	_, updated := svc.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		pool.ProbeID, pool.ProbeUntil = "", 0
		return true
	})
	require.True(t, updated)
	require.False(t, svc.claimOpenAIWSLatencyProbe(ctx, poolKey, 11, "third-probe", probeWindow), "a new batch must not start while reusable candidates remain")

	first, ok := svc.reserveOpenAIWSLatencyCandidate(ctx, poolKey, 11, "lease-one", 300)
	require.True(t, ok)
	second, ok := svc.reserveOpenAIWSLatencyCandidate(ctx, poolKey, 11, "lease-two", 300)
	require.True(t, ok)
	require.NotEqual(t, first.AccountID, second.AccountID, "parallel switches must reserve different candidates")
	_, ok = svc.reserveOpenAIWSLatencyCandidate(ctx, poolKey, 11, "lease-three", 300)
	require.False(t, ok, "a candidate already leased to another switch cannot be claimed twice")

	svc.releaseOpenAIWSLatencyCandidate(ctx, poolKey, "lease-one", first.AccountID, false)
	retried, ok := svc.reserveOpenAIWSLatencyCandidate(ctx, poolKey, 11, "lease-three", 300)
	require.True(t, ok)
	require.Equal(t, first.AccountID, retried.AccountID, "an unsuccessful switch releases its candidate for reuse")
	svc.releaseOpenAIWSLatencyCandidate(ctx, poolKey, "lease-three", retried.AccountID, true)

	pool = svc.getOpenAIWSLatencyCandidatePool(ctx, poolKey)
	require.Len(t, pool.Candidates, 1, "a successful switch consumes exactly one candidate")
	svc.releaseOpenAIWSLatencyCandidate(ctx, poolKey, "lease-two", second.AccountID, true)
	_, _ = svc.addOpenAIWSLatencyCandidates(ctx, poolKey, []openAIWSLatencyProbeResult{{accountID: 11, ms: 1000, ok: true}}, 600, 0, "", 300)
	require.True(t, svc.claimOpenAIWSLatencyProbe(ctx, poolKey, 11, "self-candidate-probe", probeWindow), "a candidate matching the current account cannot serve this trigger")
	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Equal(t, 0, cache.probeStarts, "candidate-pool coordination is separate from per-dialogue state")
}

func TestOpenAIWSLatencyRecordsAuthenticatedUserAcrossLifecycle(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	sink, cleanup := captureStructuredLog(t)
	defer cleanup()
	svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	pool := newOpenAIWSConnPool(svc.cfg)
	pool.setClientDialerForTest(&openAIWSLatencyProbeTestDialer{
		firstTokenDelay: func(headers http.Header) time.Duration {
			if headers.Get("Authorization") == "Bearer sk-slow" {
				return 500 * time.Millisecond
			}
			return 5 * time.Millisecond
		},
	})
	defer pool.Close()
	svc.openaiWSPool, svc.openaiWSResolver, svc.toolCorrector = pool, NewOpenAIWSProtocolResolver(svc.cfg), NewCodexToolCorrector()
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{
		{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{1}, Credentials: map[string]any{"api_key": "sk-slow"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}},
		{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{1}, Credentials: map[string]any{"api_key": "sk-fast"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}},
	}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, ProbeCount: 2, WaitSeconds: 73}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	ctx := context.Background()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set("thread-id", "caller-lifecycle")
	groupID := int64(1)
	payload := []byte(`{"input":[{"role":"user","content":"private question"}]}`)
	apiKey := &APIKey{ID: 7, UserID: 42, Key: "sk-private-caller", User: &User{ID: 42, Username: "caller", Email: "caller@example.com"}}
	session := svc.NewOpenAIWSLatencySession(ctx, c, payload, &groupID, apiKey, "caller-session", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
	require.NotNil(t, session)
	// Mutating the auth object must not alter the caller captured by the session.
	apiKey.User.Username, apiKey.User.Email = "changed", "changed@example.com"
	_, updated := svc.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, ProbeID: "caller-probe"}
		return true
	})
	require.True(t, updated)
	svc.probeOpenAIWSLatencyRoutes(ctx, session.key(session.model), &groupID, 11, session.model, session.model, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, "caller-probe", session.actor)
	require.Equal(t, int64(22), session.state(ctx).TargetID)
	remaining := time.Until(time.UnixMilli(session.state(ctx).PendingUntil))
	require.Greater(t, remaining, 72*time.Second)
	require.LessOrEqual(t, remaining, 73*time.Second)
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, session.BeforeRequest(ctx, 2, &Account{ID: 11}, payload, ""), &closeErr)
	apiKey.User.Username, apiKey.User.Email = "caller", "caller@example.com"
	reconnected := svc.NewOpenAIWSLatencySession(ctx, c, payload, &groupID, apiKey, "caller-session", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
	require.NoError(t, reconnected.Prepare(ctx, payload))
	reconnected.Observe(ctx, 1, &Account{ID: 22}, &OpenAIForwardResult{}, nil)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	var events []string
	for _, event := range sink.events {
		if !strings.HasPrefix(event.Message, "openai.ws_latency_switch_") {
			continue
		}
		events = append(events, event.Message)
		require.NotEmpty(t, event.Fields["conversation_id"], event.Message)
		require.Equal(t, int64(42), event.Fields["user_id"], event.Message)
		require.Equal(t, int64(7), event.Fields["api_key_id"], event.Message)
		require.Equal(t, "caller", event.Fields["username"], event.Message)
		require.Equal(t, "caller@example.com", event.Fields["user_email"], event.Message)
		raw, err := json.Marshal(event.Fields)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "sk-private-caller")
		require.NotContains(t, string(raw), "private question")
	}
	require.Equal(t, []string{
		"openai.ws_latency_switch_probe_completed",
		"openai.ws_latency_switch_pending",
		"openai.ws_latency_switch_reconnect_requested",
		"openai.ws_latency_switch_completed",
	}, events)
}

func TestOpenAIWSLatencyProbeLimiterSharesLimitAndDrainsAfterDecrease(t *testing.T) {
	var limiter openAIWSLatencyProbeLimiter
	require.True(t, limiter.tryAcquire(3))
	require.True(t, limiter.tryAcquire(3))
	require.True(t, limiter.tryAcquire(3))
	require.False(t, limiter.tryAcquire(3), "overlapping conversations share the limit")
	require.False(t, limiter.tryAcquire(2), "lowering the limit must wait for existing probes to drain")
	limiter.release()
	require.False(t, limiter.tryAcquire(2))
	limiter.release()
	require.True(t, limiter.tryAcquire(2))
	limiter.release()
	limiter.release()
	require.True(t, limiter.tryAcquire(8), "a later increase must take effect without replacing the limiter")
	limiter.release()
}

func TestOpenAIWSLatencyNativeIngressStopsBeforeOldUpstreamWrite(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	cfg := svc.cfg
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	upstream := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_A","model":"gpt-5.1"}}`)}}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: upstream})
	defer pool.Close()
	svc.openaiWSPool, svc.openaiWSResolver, svc.toolCorrector = pool, NewOpenAIWSProtocolResolver(cfg), NewCodexToolCorrector()
	account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseNow()
		_, first, err := conn.Read(r.Context())
		if err != nil {
			serverErr <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r
		groupID := int64(1)
		session := svc.NewOpenAIWSLatencySession(r.Context(), c, first, &groupID, &APIKey{ID: 1}, "test", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
		err = svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, account, "sk-test", first, &OpenAIWSIngressHooks{
			BeforeRequest: func(turn int, payload []byte, model string) error {
				return session.BeforeRequest(r.Context(), turn, account, payload, model)
			},
			AfterTurn: func(turn int, result *OpenAIForwardResult, turnErr error) {
				session.Observe(r.Context(), turn, account, result, turnErr)
				if turn == 1 {
					seedOpenAIWSLatencyPending(t, session)
				}
			},
		})
		var closeErr *OpenAIWSClientCloseError
		if errors.As(err, &closeErr) {
			_ = conn.Close(closeErr.StatusCode(), closeErr.Reason())
		}
		serverErr <- err
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &coderws.DialOptions{HTTPHeader: http.Header{"Thread-Id": []string{"native-test"}}})
	require.NoError(t, err)
	defer conn.CloseNow()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":[{"role":"user","content":"first"}]}`)))
	_, _, err = conn.Read(ctx)
	require.NoError(t, err)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_A","input":[{"role":"user","content":"blocked question"}]}`)))
	_, _, err = conn.Read(ctx)
	require.Equal(t, OpenAIWSLatencySwitchCloseCode, coderws.CloseStatus(err))
	require.Error(t, <-serverErr)
	require.Len(t, upstream.writes, 1, "the blocked question must never be written to A")
}
