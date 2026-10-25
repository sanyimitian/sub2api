package service

import (
	"context"
	"errors"
	"fmt"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAISessionPoolCommittedCandidateNeverReturnsToSharedPool(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	ctx := context.Background()
	key := openAIWSLatencyCandidatePoolKey(nil, "gpt-5.1", OpenAIUpstreamTransportHTTPSSE)
	result := openAIWSLatencyProbeResult{resultID: "one", accountID: 12, upstreamSessionID: "upstream-one", upstreamModel: "gpt-5.1", ms: 200, ok: true}
	_, reserved := svc.addOpenAIWSLatencyCandidates(ctx, key, []openAIWSLatencyProbeResult{result}, 600, 12, "owner", 300)
	require.True(t, reserved)
	require.False(t, svc.commitOpenAIWSLatencyCandidate(ctx, key, "another-owner", "one", 12))
	require.True(t, svc.commitOpenAIWSLatencyCandidate(ctx, key, "owner", "one", 12))
	svc.updateOpenAIWSLatencyCandidatePool(ctx, key, func(pool *openAIWSLatencyCandidatePool) bool {
		pool.Candidates[0].LeaseUntil = time.Now().Add(-time.Second).UnixMilli()
		return true
	})
	svc.releaseOpenAIWSLatencyCandidate(ctx, key, "owner", 12, false)
	// 重复收集同一探测结果也不能把独占状态覆盖掉。
	svc.addOpenAIWSLatencyCandidates(ctx, key, []openAIWSLatencyProbeResult{result}, 600, 0, "", 300)
	_, reserved = svc.reserveOpenAIWSLatencyCandidate(ctx, key, 0, "new-owner", 300, map[int64]struct{}{12: {}}, map[int64]string{12: "gpt-5.1"})
	require.False(t, reserved)
	candidates := svc.getOpenAIWSLatencyCandidatePool(ctx, key).Candidates
	require.Len(t, candidates, 1)
	require.True(t, candidates[0].Committed)
	svc.releaseOpenAIWSLatencyCandidate(ctx, key, "owner", 12, true)
	require.Empty(t, svc.getOpenAIWSLatencyCandidatePool(ctx, key).Candidates)
}

func TestOpenAISessionPoolRejectsExpiredLeaseBeforeSendingUserContent(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	ctx := context.Background()
	key := openAIWSLatencyCandidatePoolKey(nil, "gpt-5.1", OpenAIUpstreamTransportHTTPSSE)
	_, reserved := svc.addOpenAIWSLatencyCandidates(ctx, key, []openAIWSLatencyProbeResult{{resultID: "one", accountID: 12, upstreamSessionID: "upstream-one", upstreamModel: "gpt-5.1", ms: 200, ok: true}}, 600, 12, "owner", 300)
	require.True(t, reserved)
	svc.updateOpenAIWSLatencyCandidatePool(ctx, key, func(pool *openAIWSLatencyCandidatePool) bool {
		pool.Candidates[0].LeaseUntil = time.Now().Add(-time.Second).UnixMilli()
		return true
	})
	require.False(t, svc.commitOpenAIWSLatencyCandidate(ctx, key, "owner", "one", 12))
}

func preparedTestAccount(id int64, groups ...int64) Account {
	return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 20, GroupIDs: groups,
		Credentials: map[string]any{"api_key": "test-only", "base_url": "https://api.example.test"},
		Extra:       map[string]any{OpenAISessionPoolEnabledExtraKey: true, OpenAISessionPoolModelsExtraKey: []string{"gpt-5.1"}}}
}

func seedPreparedPool(t *testing.T, svc *OpenAIGatewayService, account Account, count int) {
	t.Helper()
	_, ok := svc.updateOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		for i := 0; i < count; i++ {
			pool.Candidates = append(pool.Candidates, openAIWSLatencyReusableCandidate{ResultID: fmt.Sprintf("%d-%d", account.ID, i), AccountID: account.ID, UpstreamSessionID: fmt.Sprintf("prepared-%d-%d", account.ID, i), UpstreamModel: "gpt-5.1", Transport: OpenAIUpstreamTransportHTTPSSE, AccountFingerprint: openAISessionPoolAccountFingerprint(&account), FirstTokenMS: 100 + i, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()})
		}
		return true
	})
	require.True(t, ok)
}

func preparedHTTPSession(svc *OpenAIGatewayService, group int64, conversation string) *OpenAIWSLatencySession {
	return svc.NewOpenAIHTTPResponsesLatencySession(context.Background(), &group, &APIKey{ID: 7, UserID: 42}, conversation, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, []byte(`{"input":"hello"}`))
}

func TestOpenAISessionPoolFirstRequestAndContinuation(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, expiresAt: time.Now().Add(time.Hour).UnixNano()}) // 慢切换关闭不影响首次领取。
	account := preparedTestAccount(11, 1, 2)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{account}}
	seedPreparedPool(t, svc, account, 2)
	ctx := context.Background()
	session := preparedHTTPSession(svc, 2, "new-user")
	retry, err := session.PrepareHTTP(ctx, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	require.False(t, retry)
	require.True(t, session.state(ctx).InitialAssignment)
	selection, _, err := session.SelectTarget(ctx, map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(11), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		defer selection.ReleaseFunc()
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, selection.Account, "gpt-5.1"))
	id := OpenAIWSLatencyUpstreamSessionID(c)
	require.Equal(t, "prepared-11-0", id)
	session.Observe(ctx, 1, selection.Account, &OpenAIForwardResult{}, nil)
	require.Zero(t, session.state(ctx).TargetID)
	require.False(t, session.recovering)
	require.Len(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates, 1)
	next := preparedHTTPSession(svc, 2, "new-user")
	retry, err = next.PrepareHTTP(ctx, []byte(`{"input":"next","previous_response_id":"resp-user"}`))
	require.NoError(t, err)
	require.False(t, retry)
	selected, decision, err := next.SelectTarget(ctx, map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.True(t, decision.StickyPreviousHit)
	if selected.ReleaseFunc != nil {
		defer selected.ReleaseFunc()
	}
	require.NoError(t, next.ApplyUpstreamSessionIdentity(ctx, c, selected.Account, "gpt-5.1"))
	require.Equal(t, id, OpenAIWSLatencyUpstreamSessionID(c))
	require.Len(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates, 1)
}

func TestOpenAISessionPoolCrossModelUsesRequestedModel(t *testing.T) {
	for _, initial := range []bool{true, false} {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("initial=%v/passthrough=%v", initial, passthrough), func(t *testing.T) {
				svc, _ := newOpenAIWSLatencyTestService(t)
				svc.cfg.Gateway.OpenAIWS.Enabled = false
				a := preparedTestAccount(11, 1)
				a.Credentials["model_mapping"] = map[string]any{"gpt-5.1": "gpt-5.1", "gpt-6.1-sol": "gpt-6.1-sol"}
				a.Extra["openai_passthrough"] = passthrough
				svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
				upstream := &openAIWSLatencyHTTPProbeUpstream{}
				svc.httpUpstream = upstream
				seedPreparedPool(t, svc, a, 1)
				ctx := context.Background()
				group := int64(1)
				payload := []byte(`{"model":"gpt-6.1-sol","stream":true,"input":"hello"}`)
				newSession := func() *OpenAIWSLatencySession {
					return svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, &APIKey{ID: 7}, "cross-model", "gpt-6.1-sol", "gpt-6.1-sol", OpenAIEndpointCapabilityResponses, payload)
				}
				if !initial {
					gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, Consecutive: 1, SlowMS: 15000}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
					trigger := newSession()
					ms := 17000
					old := preparedTestAccount(22, 1)
					trigger.Observe(ctx, 1, &old, &OpenAIForwardResult{FirstTokenMs: &ms}, nil)
					require.Equal(t, a.ID, trigger.state(ctx).TargetID)
					retry, err := newSession().PrepareHTTP(ctx, payload)
					require.NoError(t, err)
					require.True(t, retry)
				}
				session := newSession()
				retry, err := session.PrepareHTTP(ctx, payload)
				require.NoError(t, err)
				require.False(t, retry)
				require.Equal(t, initial, session.state(ctx).InitialAssignment)
				require.Equal(t, "gpt-5.1", session.state(ctx).TargetUpstreamModel, "探测模型仍作为探测元数据保存")
				selection, _, err := session.SelectTarget(ctx, map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.Equal(t, a.ID, selection.Account.ID)
				if selection.ReleaseFunc != nil {
					defer selection.ReleaseFunc()
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, selection.Account, session.model))
				result, err := svc.Forward(ctx, c, selection.Account, payload)
				require.NoError(t, err)
				require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(upstream.body, "model").String())
				require.Equal(t, "prepared-11-0", gjson.GetBytes(upstream.body, "prompt_cache_key").String())
				require.Equal(t, "prepared-11-0", upstream.request.Header.Get("session_id"))
				session.Observe(ctx, 1, selection.Account, result, nil)
				require.Equal(t, "prepared-11-0", session.state(ctx).ActiveUpstreamSessionID)
				require.Empty(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates)
			})
		}
	}
}

func TestOpenAISessionPoolNewRequestOutsideWhitelistCannotClaim(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	a.Credentials["model_mapping"] = map[string]any{"gpt-5.1": "gpt-5.1"}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	ctx := context.Background()
	group := int64(1)
	payload := []byte(`{"model":"gpt-6.1-sol","input":"hello"}`)
	session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, &APIKey{ID: 7}, "outside-whitelist", "gpt-6.1-sol", "gpt-6.1-sol", OpenAIEndpointCapabilityResponses, payload)
	retry, err := session.PrepareHTTP(ctx, payload)
	require.NoError(t, err)
	require.False(t, retry)
	require.Zero(t, session.state(ctx).TargetID)
	pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	require.Len(t, pool.Candidates, 1)
	require.Empty(t, pool.Candidates[0].LeaseID)
}

func TestOpenAISessionPoolAuthorizationModelAndEnableChanges(t *testing.T) {
	for _, kind := range []string{"unauthorized", "disabled", "credentials_changed", "model_removed", "wrong_model", "expired", "oauth"} {
		t.Run(kind, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			seedPreparedPool(t, svc, a, 1)
			group := int64(1)
			switch kind {
			case "unauthorized":
				group = 2
			case "disabled":
				a.Extra[OpenAISessionPoolEnabledExtraKey] = false
			case "credentials_changed":
				a.Credentials["api_key"] = "changed"
			case "model_removed":
				a.Extra[OpenAISessionPoolModelsExtraKey] = []string{"other"}
			case "oauth":
				a.Type = AccountTypeOAuth
			}
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			if kind == "expired" || kind == "wrong_model" {
				svc.updateOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey, func(p *openAIWSLatencyCandidatePool) bool {
					if kind == "expired" {
						p.Candidates[0].ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
					} else {
						p.Candidates[0].UpstreamModel = "other"
					}
					return true
				})
			}
			session := preparedHTTPSession(svc, group, "request")
			retry, err := session.PrepareHTTP(context.Background(), []byte(`{"input":"hello"}`))
			require.NoError(t, err)
			require.False(t, retry)
			require.Zero(t, session.state(context.Background()).TargetID)
		})
	}
}

func TestOpenAISessionPoolConcurrentFirstRequestsShareOneAssignment(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 6)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := preparedHTTPSession(svc, 1, "same")
			_, _ = session.PrepareHTTP(context.Background(), []byte(`{"input":"hello"}`))
		}()
	}
	wg.Wait()
	pool := svc.getOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey)
	leased := 0
	for _, c := range pool.Candidates {
		if c.LeaseID != "" {
			leased++
		}
	}
	require.Equal(t, 1, leased)
	require.Len(t, pool.Candidates, 6)
}

func TestOpenAISessionPoolDistinctConversationsCannotShareCandidate(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1, 2)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	first := preparedHTTPSession(svc, 1, "first")
	second := preparedHTTPSession(svc, 2, "second")
	require.True(t, first.assignPreparedCandidate(context.Background(), "gpt-5.1", 0, true))
	require.False(t, second.assignPreparedCandidate(context.Background(), "gpt-5.1", 0, true))
	old := first.state(context.Background())
	first.abandonPreparedCandidate(context.Background(), old, "test_abandon")
	require.True(t, second.assignPreparedCandidate(context.Background(), "gpt-5.1", 0, true), "未发送用户内容的租约可以归还")
}

func TestOpenAISessionPoolSlowSwitchOnlyReservesSharedCandidates(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, Consecutive: 1, SlowMS: 15000, MinTPS: 15}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	session := preparedHTTPSession(svc, 1, "existing")
	ctx := context.Background()
	ms := 17000
	session.Observe(ctx, 1, &a, &OpenAIForwardResult{FirstTokenMs: &ms, Duration: 20 * time.Second, Usage: OpenAIUsage{OutputTokens: 100}}, nil)
	state := session.state(ctx)
	require.Equal(t, a.ID, state.TargetID)
	require.False(t, state.InitialAssignment)
	require.Equal(t, openAISessionPoolKey, state.TargetPoolKey)
	require.Empty(t, state.ProbeID)
	empty := preparedHTTPSession(svc, 1, "empty")
	empty.Observe(ctx, 1, &a, &OpenAIForwardResult{FirstTokenMs: &ms, Duration: 20 * time.Second, Usage: OpenAIUsage{OutputTokens: 100}}, nil)
	require.Zero(t, empty.state(ctx).TargetID)
	require.Empty(t, empty.state(ctx).ProbeID)
}

type preparedProbeUpstream struct {
	HTTPUpstream
	calls     atomic.Int32
	active    atomic.Int32
	maximum   atomic.Int32
	fail      bool
	failModel string
}

func (u *preparedProbeUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	active := u.active.Add(1)
	defer u.active.Add(-1)
	for old := u.maximum.Load(); active > old; old = u.maximum.Load() {
		if u.maximum.CompareAndSwap(old, active) {
			break
		}
	}
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	model := gjson.GetBytes(body, "model").String()
	if u.fail || (u.failModel != "" && u.failModel == model) {
		return nil, errors.New("synthetic probe failure")
	}
	time.Sleep(2 * time.Millisecond)
	data := fmt.Sprintf(`data: {"type":"response.output_text.delta","delta":"OK"}

data: {"type":"response.completed","response":{"id":"probe","model":%q,"status":"completed"}}

`, model)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
}

func TestOpenAISessionPoolBackgroundTotalRefillAndExpiry(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Gateway.OpenAIWS.Enabled = false
	a, b := preparedTestAccount(11, 1), preparedTestAccount(22, 2)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}
	upstream := &preparedProbeUpstream{}
	svc.httpUpstream = upstream
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 6, latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2, ProbeReuseSeconds: 17}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	ctx := context.Background()
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); svc.refillOpenAISessionPool(ctx) }()
	}
	workers.Wait()
	pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	require.Len(t, pool.Candidates, 6)
	require.EqualValues(t, 6, upstream.calls.Load())
	require.LessOrEqual(t, upstream.maximum.Load(), int32(2))
	for _, c := range pool.Candidates {
		require.Greater(t, c.ExpiresAt, time.Now().Add(15*time.Second).UnixMilli())
		require.LessOrEqual(t, c.ExpiresAt, time.Now().Add(17*time.Second).UnixMilli())
	}
	session := preparedHTTPSession(svc, pool.Candidates[0].AccountID/11, "claimed")
	// 两个账号的组分别是 1、2。
	_, _, ok := session.reservePreparedCandidate(ctx, "gpt-5.1")
	require.True(t, ok)
	svc.refillOpenAISessionPool(ctx)
	require.EqualValues(t, 7, upstream.calls.Load())
	svc.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(p *openAIWSLatencyCandidatePool) bool {
		for i := range p.Candidates {
			p.Candidates[i].ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
		}
		return true
	})
	svc.refillOpenAISessionPool(ctx)
	require.Len(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates, 6)
	require.EqualValues(t, 13, upstream.calls.Load())
}

func TestOpenAISessionPoolFailuresStopAfterFiveRounds(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Gateway.OpenAIWS.Enabled = false
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	u := &preparedProbeUpstream{fail: true}
	svc.httpUpstream = u
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 6, latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	svc.refillOpenAISessionPool(context.Background())
	require.EqualValues(t, 10, u.calls.Load())
	pool := svc.getOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey)
	require.Empty(t, pool.Candidates)
	require.Greater(t, pool.AccountProbes[a.ID].RetryAfter, time.Now().UnixMilli())
	require.Empty(t, pool.ProbeID)
	svc.refillOpenAISessionPool(context.Background())
	require.EqualValues(t, 10, u.calls.Load())
}

func TestOpenAISessionPoolOutboundIdentityAndProbeCancellation(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	a.Credentials["header_overrides"] = map[string]any{"session-id": "configured-old", "session_id": "configured-old"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session-id", "client-old")
	c.Request.Header.Set("thread-id", "thread-old")
	c.Set(openAIPreparedSessionContextKey, true)
	c.Set(openAIWSLatencyUpstreamSessionContextKey, "prepared-sticky")
	body := []byte(`{"model":"gpt-5.1","input":"hello","prompt_cache_key":"old"}`)
	normal, err := svc.buildUpstreamRequest(context.Background(), c, &a, body, "token", true, "old", false)
	require.NoError(t, err)
	passthrough, err := svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, &a, body, "token")
	require.NoError(t, err)
	ws, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, &a, "token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, false, "", "", "old", "gpt-5.1", "")
	require.NoError(t, err)
	for _, h := range []http.Header{normal.Header, passthrough.Header, ws} {
		for _, key := range []string{"session-id", "thread-id", "session_id", "conversation_id"} {
			require.Equal(t, "prepared-sticky", h.Get(key))
		}
	}
	for _, r := range []*http.Request{normal, passthrough} {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "prepared-sticky", gjson.GetBytes(raw, "prompt_cache_key").String())
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), openAIPreparedProbeContextKey{}, true))
	detached, release := detachUpstreamContext(ctx)
	defer release()
	cancel()
	require.ErrorIs(t, detached.Err(), context.Canceled)
}

func TestOpenAISessionPoolEmptyFirstRequestDoesNotClaimLater(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	ctx := context.Background()
	first := preparedHTTPSession(svc, 1, "empty-first")
	retry, err := first.PrepareHTTP(ctx, []byte("{\"input\":\"hello\"}"))
	require.NoError(t, err)
	require.False(t, retry)
	require.True(t, first.state(ctx).InitialDecisionMade)
	seedPreparedPool(t, svc, a, 1)
	next := preparedHTTPSession(svc, 1, "empty-first")
	retry, err = next.PrepareHTTP(ctx, []byte("{\"input\":\"next\"}"))
	require.NoError(t, err)
	require.False(t, retry)
	require.Zero(t, next.state(ctx).TargetID)
	require.Empty(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates[0].LeaseID)
}

func TestOpenAISessionPoolSlowSwitchCompletesOnActualCandidate(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		t.Run(fmt.Sprint(oauth), func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			old := a
			if oauth {
				old.ID, old.Type = 22, AccountTypeOAuth
			}
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			seedPreparedPool(t, svc, a, 2)
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, Consecutive: 1, SlowMS: 15000, MinTPS: 15}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
			ctx := context.Background()
			trigger := preparedHTTPSession(svc, 1, "slow")
			ms := 17000
			trigger.Observe(ctx, 1, &old, &OpenAIForwardResult{FirstTokenMs: &ms, Duration: 20 * time.Second, Usage: OpenAIUsage{OutputTokens: 100}}, nil)
			require.Equal(t, a.ID, trigger.state(ctx).TargetID)
			payload := []byte("{\"input\":\"complete context\"}")
			reconnect := preparedHTTPSession(svc, 1, "slow")
			retry, err := reconnect.PrepareHTTP(ctx, payload)
			require.NoError(t, err)
			require.True(t, retry)
			resumed := preparedHTTPSession(svc, 1, "slow")
			retry, err = resumed.PrepareHTTP(ctx, payload)
			require.NoError(t, err)
			require.False(t, retry)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			require.NoError(t, resumed.ApplyUpstreamSessionIdentity(ctx, c, &a, "gpt-5.1"))
			require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
			resumed.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
			require.Zero(t, resumed.state(ctx).TargetID)
			require.Equal(t, "prepared-11-0", resumed.state(ctx).ActiveUpstreamSessionID)
			require.Len(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates, 1)
		})
	}
}

func TestOpenAISessionPoolLateResultCannotCompleteAnotherCandidateOnSameAccount(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 2)
	ctx := context.Background()
	old := preparedHTTPSession(svc, 1, "same-account")
	_, err := old.PrepareHTTP(ctx, []byte("{\"input\":\"hello\"}"))
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, old.ApplyUpstreamSessionIdentity(ctx, c, &a, "gpt-5.1"))
	old.abandonPreparedCandidate(ctx, old.state(ctx), "test_abandon")
	// 模拟并发重试已经领取并发送新的候选；旧请求此时才完成。
	svc.updateOpenAIWSLatencyState(ctx, old.key(old.model), func(state *openAIWSLatencyState) bool { state.AccountID = a.ID; return true })
	newer := preparedHTTPSession(svc, 1, "same-account")
	require.True(t, newer.assignPreparedCandidate(ctx, newer.model, a.ID, false))
	newer.recovering, newer.recoveryConfirmed = true, true
	require.NoError(t, newer.ApplyUpstreamSessionIdentity(ctx, c, &a, newer.model))
	old.recovering, old.recoveryConfirmed = true, true
	old.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	require.Equal(t, "11-1", newer.state(ctx).TargetResultID)
	newer.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	require.Zero(t, newer.state(ctx).TargetID)
	require.Equal(t, "prepared-11-1", newer.state(ctx).ActiveUpstreamSessionID)
}

func TestOpenAISessionPoolDisablePreservesActiveAndFailoverClearsOldRoute(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 2)
	ctx := context.Background()
	first := preparedHTTPSession(svc, 1, "active")
	_, err := first.PrepareHTTP(ctx, []byte("{\"input\":\"hello\"}"))
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, first.ApplyUpstreamSessionIdentity(ctx, c, &a, first.model))
	first.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	a.Extra[OpenAISessionPoolEnabledExtraKey] = false
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	next := preparedHTTPSession(svc, 1, "active")
	_, err = next.PrepareHTTP(ctx, []byte("{\"input\":\"next\",\"previous_response_id\":\"resp-1\"}"))
	require.NoError(t, err)
	selected, _, err := next.SelectTarget(ctx, map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
	require.NoError(t, err)
	require.NotNil(t, selected)
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
	require.NoError(t, next.ApplyUpstreamSessionIdentity(ctx, c, &a, next.model))
	require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
	fresh := preparedHTTPSession(svc, 1, "fresh-disabled")
	_, err = fresh.PrepareHTTP(ctx, []byte("{\"input\":\"hello\"}"))
	require.NoError(t, err)
	require.Zero(t, fresh.state(ctx).TargetID)
	failedOver := preparedHTTPSession(svc, 1, "active")
	other := preparedTestAccount(22, 1)
	require.NoError(t, failedOver.ApplyUpstreamSessionIdentity(ctx, c, &other, failedOver.model))
	require.Empty(t, OpenAIWSLatencyUpstreamSessionID(c))
	failedOver.Observe(ctx, 1, &other, &OpenAIForwardResult{}, nil)
	// 在途旧请求完成不能写回旧线路。
	next.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	require.Equal(t, other.ID, next.state(ctx).AccountID)
	require.Empty(t, next.state(ctx).ActiveUpstreamSessionID)
}

func TestOpenAISessionPoolNativeWebSocketKeepsPreparedIdentityAcrossTurns(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	cfg := svc.cfg
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	upstream := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_A","model":"gpt-6.1-sol","status":"completed"}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_B","model":"gpt-5.1","status":"completed"}}`),
	}}
	pool := newOpenAIWSConnPool(cfg)
	dialer := &openAIWSCaptureDialer{conn: upstream}
	pool.setClientDialerForTest(dialer)
	defer pool.Close()
	svc.openaiWSPool, svc.openaiWSResolver, svc.toolCorrector = pool, NewOpenAIWSProtocolResolver(cfg), NewCodexToolCorrector()
	a := preparedTestAccount(11, 1)
	a.Extra["responses_websockets_v2_enabled"] = true
	a.Credentials["model_mapping"] = map[string]any{"gpt-5.1": "gpt-5.1", "gpt-6.1-sol": "gpt-6.1-sol"}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	svc.updateOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey, func(p *openAIWSLatencyCandidatePool) bool {
		p.Candidates[0].Transport = OpenAIUpstreamTransportResponsesWebsocketV2
		return true
	})
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		defer conn.CloseNow()
		_, first, err := conn.Read(r.Context())
		if err != nil {
			done <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r
		group := int64(1)
		session := svc.NewOpenAIWSLatencySession(r.Context(), c, first, &group, &APIKey{ID: 7}, "native", "gpt-6.1-sol", "gpt-6.1-sol", OpenAIEndpointCapabilityResponses)
		if err = session.Prepare(r.Context(), first); err != nil {
			done <- err
			return
		}
		if err = session.ApplyUpstreamSessionIdentity(r.Context(), c, &a, "gpt-6.1-sol"); err != nil {
			done <- err
			return
		}
		done <- svc.ProxyResponsesWebSocketFromClient(r.Context(), c, conn, &a, "test-only", first, &OpenAIWSIngressHooks{
			BeforeRequest: func(turn int, payload []byte, model string) error {
				if err := session.BeforeRequest(r.Context(), turn, &a, payload, model); err != nil {
					return err
				}
				return session.ApplyUpstreamSessionIdentity(r.Context(), c, &a, model, turn)
			},
			AfterTurn: func(turn int, result *OpenAIForwardResult, err error) {
				session.Observe(r.Context(), turn, &a, result, err)
			},
		})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &coderws.DialOptions{HTTPHeader: http.Header{"Thread-Id": []string{"prepared-native"}}})
	require.NoError(t, err)
	for _, payload := range []string{
		`{"type":"response.create","model":"gpt-6.1-sol","prompt_cache_key":"client-old","input":[{"role":"user","content":"first"}]}`,
		`{"type":"response.create","model":"gpt-5.1","prompt_cache_key":"client-new","previous_response_id":"resp_A","input":[{"role":"user","content":"next"}]}`,
	} {
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(payload)))
		_, _, err = conn.Read(ctx)
		require.NoError(t, err)
	}
	conn.CloseNow()
	<-done
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	require.Len(t, upstream.writes, 2)
	for i, payload := range upstream.writes {
		require.Equal(t, "prepared-11-0", payload["prompt_cache_key"])
		require.Equal(t, []string{"gpt-6.1-sol", "gpt-5.1"}[i], payload["model"], "沿用探测会话时仍按每轮用户请求选择模型")
	}
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	require.Equal(t, "prepared-11-0", dialer.lastHeaders.Get("session_id"))
	require.Empty(t, svc.getOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey).Candidates)
}

func TestOpenAISessionPoolModelChangeKeepsConversationRoute(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 2)
	ctx := context.Background()
	first := preparedHTTPSession(svc, 1, "model-change")
	_, err := first.PrepareHTTP(ctx, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, first.ApplyUpstreamSessionIdentity(ctx, c, &a, first.model))
	first.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	group := int64(1)
	next := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, &APIKey{ID: 7}, "model-change", "other-model", "other-model", OpenAIEndpointCapabilityResponses, []byte(`{"input":"next"}`))
	retry, err := next.PrepareHTTP(ctx, []byte(`{"input":"next","previous_response_id":"resp-1"}`))
	require.NoError(t, err)
	require.False(t, retry)
	require.NoError(t, next.ApplyUpstreamSessionIdentity(ctx, c, &a, "other-model"))
	require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
	require.Len(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates, 1)
}

func TestOpenAISessionPoolPrewarmWaitsForRealFirstTurn(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	ctx := context.Background()
	group := int64(1)
	warm := []byte(`{"generate":false,"input":[]}`)
	session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, &APIKey{ID: 7}, "warm", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, warm)
	retry, err := session.PrepareHTTP(ctx, warm)
	require.NoError(t, err)
	require.False(t, retry)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, session.model))
	session.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	require.True(t, session.state(ctx).InitialAssignment)
	payload := []byte(`{"input":"hello"}`)
	require.NoError(t, session.BeforeRequest(ctx, 2, &a, payload, session.model))
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, session.model, 2))
	session.Observe(ctx, 2, &a, &OpenAIForwardResult{}, nil)
	require.Zero(t, session.state(ctx).TargetID)
	require.Equal(t, "prepared-11-0", session.state(ctx).ActiveUpstreamSessionID)
}

func TestOpenAISessionPoolTrimKeepsLeasedAndFastestIdle(t *testing.T) {
	a := preparedTestAccount(11, 1)
	svc, _ := newOpenAIWSLatencyTestService(t)
	seedPreparedPool(t, svc, a, 8)
	ctx := context.Background()
	pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	pool.Candidates[7].LeaseID = "in-use"
	pool.Candidates[7].LeaseUntil = time.Now().Add(time.Minute).UnixMilli()
	routes := []openAISessionPoolRoute{{account: &a, upstreamModel: "gpt-5.1", transport: OpenAIUpstreamTransportHTTPSSE, fingerprint: openAISessionPoolAccountFingerprint(&a)}}
	require.Equal(t, 6, trimOpenAISessionPool(&pool, routes, time.Now().UnixMilli(), 6))
	require.Len(t, pool.Candidates, 7)
	require.Equal(t, "in-use", pool.Candidates[6].LeaseID)
	require.Equal(t, "11-5", pool.Candidates[5].ResultID)
}

func TestOpenAISessionPoolPassthroughPreservesIdentityAcrossFrames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	account := passthroughLifecycleAccount()
	server, done := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
		c.Set(openAIPreparedSessionContextKey, true)
		c.Set(openAIWSLatencyUpstreamSessionContextKey, "prepared-passthrough")
		return nil
	})
	defer server.Close()
	conn := dialPassthroughLifecycleClient(t, server)
	defer conn.CloseNow()
	first := requirePassthroughUpstreamWrite(t, upstream, time.Second)
	require.Equal(t, "prepared-passthrough", gjson.GetBytes(first, "prompt_cache_key").String())
	upstream.Send(`{"type":"response.completed","response":{"id":"resp-1","model":"gpt-5.1"}}`)
	_, err := readPassthroughLifecycleFrame(t, conn, 3*time.Second)
	require.NoError(t, err)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","prompt_cache_key":"client-changed","input":"next"}`)))
	next := requirePassthroughUpstreamWrite(t, upstream, time.Second)
	require.Equal(t, "prepared-passthrough", gjson.GetBytes(next, "prompt_cache_key").String())
	conn.CloseNow()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("透传未按时结束")
	}
}

func TestOpenAISessionPoolPendingCandidateChecksRequestedModelWhitelist(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		for _, atSend := range []bool{false, true} {
			t.Run(fmt.Sprintf("allowed=%v/at_send=%v", allowed, atSend), func(t *testing.T) {
				svc, _ := newOpenAIWSLatencyTestService(t)
				a := preparedTestAccount(11, 1)
				a.Credentials["model_mapping"] = map[string]any{"gpt-5.1": "gpt-5.1"}
				if allowed {
					a.Credentials["model_mapping"].(map[string]any)["other-model"] = "other-model"
				}
				svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
				seedPreparedPool(t, svc, a, 1)
				ctx := context.Background()
				first := preparedHTTPSession(svc, 1, "pending-model-change")
				_, err := first.PrepareHTTP(ctx, []byte(`{"input":"hello"}`))
				require.NoError(t, err)
				if atSend {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					err = first.ApplyUpstreamSessionIdentity(ctx, c, &a, "other-model")
					if allowed {
						require.NoError(t, err)
						require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
					} else {
						require.Error(t, err)
						require.Empty(t, OpenAIWSLatencyUpstreamSessionID(c))
					}
				} else {
					group := int64(1)
					next := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, &APIKey{ID: 7}, "pending-model-change", "other-model", "other-model", OpenAIEndpointCapabilityResponses, []byte(`{"input":"hello"}`))
					_, err = next.PrepareHTTP(ctx, []byte(`{"input":"hello"}`))
					require.NoError(t, err)
					selection, _, err := next.SelectTarget(ctx, map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
					require.NoError(t, err)
					if allowed {
						require.NotNil(t, selection)
						require.Equal(t, a.ID, selection.Account.ID)
						if selection.ReleaseFunc != nil {
							defer selection.ReleaseFunc()
						}
					} else {
						require.Nil(t, selection)
					}
				}
				pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
				require.Len(t, pool.Candidates, 1)
				if allowed {
					require.Equal(t, a.ID, first.state(ctx).TargetID)
					require.NotEmpty(t, pool.Candidates[0].LeaseID)
					require.Equal(t, atSend, pool.Candidates[0].Committed)
				} else {
					require.Zero(t, first.state(ctx).TargetID)
					require.Empty(t, pool.Candidates[0].LeaseID)
					require.False(t, pool.Candidates[0].Committed)
				}
			})
		}
	}
}

func TestOpenAISessionPoolUnsentCandidateCannotTakeOverIncrementalContext(t *testing.T) {
	for _, payload := range []string{
		`{"input":"next","previous_response_id":"resp-old"}`,
		`{"input":[{"type":"function_call_output","call_id":"call-old","output":"result"}]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			seedPreparedPool(t, svc, a, 1)
			ctx := context.Background()
			first := preparedHTTPSession(svc, 1, "incremental")
			_, err := first.PrepareHTTP(ctx, []byte(`{"generate":false,"input":[]}`))
			require.NoError(t, err)
			require.NotZero(t, first.state(ctx).TargetID)
			next := preparedHTTPSession(svc, 1, "incremental")
			retry, err := next.PrepareHTTP(ctx, []byte(payload))
			require.NoError(t, err)
			require.False(t, retry)
			selection, _, err := next.SelectTarget(ctx, map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
			require.NoError(t, err)
			require.Nil(t, selection)
			require.Zero(t, next.state(ctx).TargetID)
			pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
			require.Len(t, pool.Candidates, 1)
			require.Empty(t, pool.Candidates[0].LeaseID)
		})
	}
}

func TestOpenAISessionPoolHTTPAndWebSocketShareConversationBinding(t *testing.T) {
	for _, legacyHTTP := range []bool{false, true} {
		t.Run(fmt.Sprint(legacyHTTP), func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			seedPreparedPool(t, svc, a, 2)
			ctx := context.Background()
			group, key := int64(1), &APIKey{ID: 7}
			payload := []byte(`{"input":"hello","model":"gpt-5.1"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("session_id", "shared-client-session")
			c.Request.Header.Set("thread-id", "shared-client-thread")
			hash := svc.GenerateSessionHash(c, payload)
			contexts := []*gin.Context{c}
			if legacyHTTP {
				contexts = nil
			}
			first := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload, contexts...)
			_, err := first.PrepareHTTP(ctx, payload)
			require.NoError(t, err)
			require.NoError(t, first.ApplyUpstreamSessionIdentity(ctx, c, &a, "gpt-5.1"))
			first.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
			next := svc.NewOpenAIWSLatencySession(ctx, c, payload, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
			require.NotNil(t, next)
			require.NoError(t, next.Prepare(ctx, payload))
			require.NoError(t, next.ApplyUpstreamSessionIdentity(ctx, c, &a, "gpt-5.1"))
			require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
			again := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload, c)
			require.Equal(t, next.key("gpt-5.1"), again.key("gpt-5.1"))
			require.Equal(t, "prepared-11-0", again.state(ctx).ActiveUpstreamSessionID)
			require.Len(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates, 1)
		})
	}
}

func TestOpenAISessionPoolCompactionOnlyUsesExistingBinding(t *testing.T) {
	for _, stage := range []string{"empty", "reserved", "committed", "active", "switch_pending"} {
		t.Run(stage, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			seedPreparedPool(t, svc, a, 3)
			ctx := context.Background()
			first := preparedHTTPSession(svc, 1, "compact")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if stage != "empty" {
				_, err := first.PrepareHTTP(ctx, []byte(`{"input":"hello"}`))
				require.NoError(t, err)
			}
			if stage == "committed" || stage == "active" || stage == "switch_pending" {
				require.NoError(t, first.ApplyUpstreamSessionIdentity(ctx, c, &a, first.model))
			}
			if stage == "active" || stage == "switch_pending" {
				first.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
			}
			if stage == "switch_pending" {
				require.True(t, first.assignPreparedCandidate(ctx, first.model, a.ID, false))
			}
			before := first.state(ctx)
			beforePool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
			compact := preparedHTTPSession(svc, 1, "compact")
			compact.PrepareExistingBinding(false)
			selection, _, err := compact.SelectTarget(ctx, map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
			require.NoError(t, err)
			if stage == "empty" || stage == "reserved" {
				require.Nil(t, selection)
				require.NoError(t, compact.ApplyUpstreamSessionIdentity(ctx, c, &a, compact.model))
				require.Empty(t, OpenAIWSLatencyUpstreamSessionID(c))
			} else {
				require.NotNil(t, selection)
				if selection.ReleaseFunc != nil {
					defer selection.ReleaseFunc()
				}
				require.NoError(t, compact.ApplyUpstreamSessionIdentity(ctx, c, selection.Account, compact.model))
				require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
			}
			require.Equal(t, before, first.state(ctx), "压缩不能完成或放弃待接管候选")
			require.Equal(t, beforePool.Candidates, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates)
		})
	}
}

func TestOpenAISessionPoolStableClientIdentityIsRequiredAndThreadsAreIsolated(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 2)
	ctx := context.Background()
	group, key := int64(1), &APIKey{ID: 7}
	payload := []byte(`{"input":"same message","model":"gpt-5.1"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	hash := svc.GenerateSessionHash(c, payload)
	require.NotEmpty(t, hash)
	diagnosticCtx, logs := poolDiagnosticTestContext()
	missing := svc.NewOpenAIHTTPResponsesLatencySession(diagnosticCtx, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload, c)
	require.Nil(t, missing, "相同文本不能被用作可共享用户状态的会话身份")
	skipped := logs.FilterMessage("openai.latency_diagnostic").All()
	require.Len(t, skipped, 1)
	require.Equal(t, "session_scope_missing", skipped[0].ContextMap()["reason"])
	pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	require.Len(t, pool.Candidates, 2)
	for _, candidate := range pool.Candidates {
		require.Empty(t, candidate.LeaseID, "缺少会话身份时走正常调度，不占用共享候选")
	}
	c.Request.Header.Set("session-id", "parent-session")
	ids := make(map[string]bool)
	for _, thread := range []string{"thread-one", "thread-two"} {
		c.Request.Header.Set("thread-id", thread)
		hash = svc.GenerateSessionHash(c, payload)
		session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload, c)
		require.NotNil(t, session)
		_, err := session.PrepareHTTP(ctx, payload)
		require.NoError(t, err)
		require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, session.model))
		id := OpenAIWSLatencyUpstreamSessionID(c)
		require.NotEmpty(t, id)
		require.False(t, ids[id], "共用父会话标识的两个线程必须独占不同候选")
		ids[id] = true
	}
}

func TestOpenAISessionPoolThreadOnlyPrewarmAndContinuation(t *testing.T) {
	for _, identity := range []string{"header", "body"} {
		t.Run(identity, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			seedPreparedPool(t, svc, a, 1)
			ctx := context.Background()
			group, key := int64(1), &APIKey{ID: 7}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			metadata := ""
			if identity == "header" {
				c.Request.Header.Set("thread-id", "private-thread")
			} else {
				metadata = `,"client_metadata":{"thread_id":"private-thread"}`
			}
			var firstHash, firstScope string
			for i, input := range []string{`"generate":false,"input":[]`, `"input":"hello"`, `"input":"different follow-up"`} {
				payload := []byte(`{"model":"gpt-5.1",` + input + metadata + `}`)
				hash := svc.GenerateSessionHash(c, payload)
				require.NotEmpty(t, hash, "空输入预热也必须识别已有线程标识")
				session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload, c)
				require.NotNil(t, session)
				if i == 0 {
					firstHash, firstScope = hash, session.scope
				} else {
					require.Equal(t, firstHash, hash)
					require.Equal(t, firstScope, session.scope)
				}
				_, err := session.PrepareHTTP(ctx, payload)
				require.NoError(t, err)
				require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, session.model))
				require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
			}
		})
	}
}

func TestOpenAISessionPoolWebSocketCompactionKeepsBindingWithoutSwitching(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 2)
	ctx := context.Background()
	group, key := int64(1), &APIKey{ID: 7}
	payload := []byte(`{"input":"hello","model":"gpt-5.1"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session_id", "compact-shared")
	c.Request.Header.Set("thread-id", "compact-thread")
	hash := svc.GenerateSessionHash(c, payload)
	first := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload, c)
	_, err := first.PrepareHTTP(ctx, payload)
	require.NoError(t, err)
	require.NoError(t, first.ApplyUpstreamSessionIdentity(ctx, c, &a, first.model))
	first.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	require.True(t, first.assignPreparedCandidate(ctx, first.model, a.ID, false))
	before := first.state(ctx)
	c.Request.Header.Set(openAIWSTurnMetadataHeader, `{"thread_id":"compact-thread","request_kind":"compaction"}`)
	compact := svc.NewOpenAIWSLatencySession(ctx, c, payload, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
	require.NotNil(t, compact)
	require.True(t, compact.existingBindingOnly)
	require.NoError(t, compact.Prepare(ctx, payload))
	require.NoError(t, compact.BeforeRequest(ctx, 1, &a, payload, compact.model))
	require.NoError(t, compact.ApplyUpstreamSessionIdentity(ctx, c, &a, compact.model))
	require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
	compact.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	require.Equal(t, before, first.state(ctx))
}

func TestOpenAISessionPoolDisabledDoesNotProbeAllocateOrRewrite(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{expiresAt: time.Now().Add(time.Hour).UnixNano()})
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	upstream := &preparedProbeUpstream{}
	svc.httpUpstream = upstream
	seedPreparedPool(t, svc, a, 1)
	ctx := context.Background()
	before := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	svc.refillOpenAISessionPool(ctx)
	require.Zero(t, upstream.calls.Load(), "总开关关闭，即使账号开关已开启，也不能后台探测")
	group := int64(1)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session-id", "original-session")
	payload := []byte(`{"input":"hello","model":"gpt-5.1"}`)
	httpSession := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, &APIKey{ID: 7}, "hash", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload, c)
	wsSession := svc.NewOpenAIWSLatencySession(ctx, c, payload, &group, &APIKey{ID: 7}, "hash", "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses)
	require.Nil(t, httpSession)
	require.Nil(t, wsSession)
	require.NoError(t, httpSession.ApplyUpstreamSessionIdentity(ctx, c, &a, "gpt-5.1"))
	headers := http.Header{"Session-Id": []string{"original-session"}, "Thread-Id": []string{"original-thread"}}
	applyOpenAIPreparedSessionHeaders(c, headers)
	require.Equal(t, "original-session", headers.Get("session-id"))
	require.Equal(t, "original-thread", headers.Get("thread-id"))
	require.Empty(t, headers.Get("conversation_id"))
	require.Equal(t, before.Candidates, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.states, 1, "关闭时不创建备用对话状态，只保留预置池数据")
}

func TestOpenAISessionPoolDisabledKeepsLegacySlowSwitch(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, Consecutive: 1, SlowMS: 15000, MinTPS: 15}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	a := preparedTestAccount(11, 1)
	a.Extra[OpenAISessionPoolEnabledExtraKey] = false
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	ctx := context.Background()
	session := preparedHTTPSession(svc, 1, "legacy-slow")
	require.NotNil(t, session.legacy)
	require.Equal(t, fmt.Sprintf("1:%s:%s", session.scope, hashOpenAIWSLatencyModel(session.model)), session.key(session.model))
	legacyPool := openAIWSLatencyCandidatePoolKey(session.groupID, session.model, OpenAIUpstreamTransportHTTPSSE)
	svc.addOpenAIWSLatencyCandidates(ctx, legacyPool, []openAIWSLatencyProbeResult{{resultID: "legacy-fast", accountID: a.ID, upstreamSessionID: "legacy-upstream", upstreamModel: session.model, ms: 100, ok: true}}, 600, 0, "", 300)
	ms := 16000
	session.Observe(ctx, 1, &a, &OpenAIForwardResult{FirstTokenMs: &ms, Duration: 20 * time.Second, Usage: OpenAIUsage{OutputTokens: 100}}, nil)
	require.Eventually(t, func() bool { return session.state(ctx).TargetResultID == "legacy-fast" }, time.Second, time.Millisecond)
	state := session.state(ctx)
	require.Equal(t, legacyPool, state.TargetPoolKey)
	require.Equal(t, a.ID, state.TargetID)
	require.Empty(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates)
	next := preparedHTTPSession(svc, 1, "legacy-slow")
	retry, err := next.PrepareHTTP(ctx, []byte(`{"input":"next"}`))
	require.NoError(t, err)
	require.True(t, retry)
}

func TestOpenAISessionPoolLegacyHeadersKeepOriginalOverrideOrder(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(openAIWSLatencyUpstreamSessionContextKey, "legacy-id")
	h := http.Header{"Session-Id": []string{"original"}, "Thread-Id": []string{"thread"}}
	applyOpenAIPreparedSessionHeaders(c, h)
	require.Equal(t, "legacy-id", h.Get("session_id"))
	require.Equal(t, "legacy-id", h.Get("conversation_id"))
	require.Equal(t, "original", h.Get("session-id"))
	require.Equal(t, "thread", h.Get("thread-id"))
	require.Empty(t, OpenAIPreparedSessionID(c))
}

func TestOpenAISessionPoolDisableStopsExistingReservations(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	session := preparedHTTPSession(svc, 1, "disable-before-send")
	ctx := context.Background()
	_, err := session.PrepareHTTP(ctx, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{expiresAt: time.Now().Add(time.Hour).UnixNano()})
	selection, _, err := session.SelectTarget(ctx, map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
	require.NoError(t, err)
	require.Nil(t, selection)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, session.model))
	require.Empty(t, OpenAIWSLatencyUpstreamSessionID(c))
	_, _, ok := session.reservePreparedCandidate(ctx, session.model)
	require.False(t, ok)
	require.False(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates[0].Committed)
}
