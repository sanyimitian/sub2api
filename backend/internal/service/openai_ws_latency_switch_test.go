package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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
	if oldState.ProbeID == "" && newState.ProbeID != "" {
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
	session := svc.NewOpenAIWSLatencySession(context.Background(), c, payload, &groupID, apiKeyID, "test-session", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
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

func TestOpenAIWSLatencyReconnectPreservesPendingAcrossInstances(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	payload := []byte(`{"input":[{"role":"user","content":"question"}]}`)
	old := newOpenAIWSLatencyTestSession(t, svc, "reconnect", 1, payload)
	seedOpenAIWSLatencyPending(t, old)
	err := old.BeforeRequest(context.Background(), 2, &Account{ID: 11}, payload, "")
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.StatusCode())
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

func TestOpenAIWSLatencyTargetRequiresMeaningfulImprovement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []openAIWSLatencyProbeResult
		want    int64
	}{
		{"faster", []openAIWSLatencyProbeResult{{accountID: 1, ms: 5000, ok: true}, {accountID: 2, ms: 1500, ok: true}}, 2},
		{"jitter", []openAIWSLatencyProbeResult{{accountID: 1, ms: 1000, ok: true}, {accountID: 2, ms: 900, ok: true}}, 0},
		{"no baseline", []openAIWSLatencyProbeResult{{accountID: 2, ms: 100, ok: true}}, 0},
		{"failed candidate", []openAIWSLatencyProbeResult{{accountID: 1, ms: 5000, ok: true}, {accountID: 2, ms: 100}}, 0},
		{"baseline timeout", []openAIWSLatencyProbeResult{{accountID: 1, ms: 15000, timedOut: true}, {accountID: 2, ms: 1500, ok: true}}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, chooseOpenAIWSLatencyTarget(tc.results, 1)) })
	}
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

type openAIWSLatencyProbeTestDialer struct {
	mu    sync.Mutex
	calls int
}

func (dialer *openAIWSLatencyProbeTestDialer) Dial(_ context.Context, _ string, _ http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	dialer.mu.Lock()
	dialer.calls++
	dialer.mu.Unlock()
	return &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.output_text.delta","delta":"OK"}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_probe","model":"gpt-5.1","status":"completed"}}`),
	}}, 0, nil, nil
}

func TestOpenAIWSLatencyProbeCountChangesBetweenBatches(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc.cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	svc.cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
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
		svc.probeOpenAIWSLatencyRoutes(ctx, fmt.Sprintf("probe-batch-%d", batch), &groupID, accounts[0].ID, "gpt-5.1", OpenAIEndpointCapabilityResponses, "test-probe")
		cancel()
		want := count
		if want == 0 {
			want = 6
		}
		if want > len(accounts) {
			want = len(accounts)
		}
		total += want
		dialer.mu.Lock()
		calls := dialer.calls
		dialer.mu.Unlock()
		require.Equal(t, total, calls, "batch %d with configured probe count %d", batch, count)
	}
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
		session := svc.NewOpenAIWSLatencySession(r.Context(), c, first, &groupID, 1, "test", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
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
	require.Equal(t, coderws.StatusTryAgainLater, coderws.CloseStatus(err))
	require.Error(t, <-serverErr)
	require.Len(t, upstream.writes, 1, "the blocked question must never be written to A")
}
