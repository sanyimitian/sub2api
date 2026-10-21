package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 精确安排两请求的交错：B 已读旧状态，A 完成并消费候选，B 才读取池准备独占。
type preparedReviewCache struct {
	*openAIWSLatencyMemoryCache
	beforePoolRead   func()
	beforeStateWrite func()
	failStateRead    bool
	failStateWrite   bool
	failWriteKey     string
	once             sync.Once
	stateWriteOnce   sync.Once
}

func (cache *preparedReviewCache) GetOpenAIWSLatencyState(ctx context.Context, key string) ([]byte, error) {
	if key == openAISessionPoolKey && cache.beforePoolRead != nil {
		cache.once.Do(func() {
			callback := cache.beforePoolRead
			cache.beforePoolRead = nil
			callback()
		})
	}
	if key != openAISessionPoolKey && cache.failStateRead {
		return nil, errors.New("state cache unavailable")
	}
	return cache.openAIWSLatencyMemoryCache.GetOpenAIWSLatencyState(ctx, key)
}

func (cache *preparedReviewCache) CompareAndSwapOpenAIWSLatencyState(ctx context.Context, key string, expected, next []byte, ttl time.Duration) (bool, error) {
	if key != openAISessionPoolKey && cache.beforeStateWrite != nil {
		cache.stateWriteOnce.Do(cache.beforeStateWrite)
	}
	if (key != openAISessionPoolKey && cache.failStateWrite) || (cache.failWriteKey != "" && key == cache.failWriteKey) {
		return false, errors.New("state write unavailable")
	}
	return cache.openAIWSLatencyMemoryCache.CompareAndSwapOpenAIWSLatencyState(ctx, key, expected, next, ttl)
}

func TestOpenAISessionPoolConcurrentCompletionKeepsPreparedRoute(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	ctx := context.Background()
	first := preparedHTTPSession(svc, 1, "concurrent-completion")
	_, err := first.PrepareHTTP(ctx, []byte("{\"input\":\"hello\"}"))
	require.NoError(t, err)
	second := preparedHTTPSession(svc, 1, "concurrent-completion")
	_, err = second.PrepareHTTP(ctx, []byte("{\"input\":\"hello\"}"))
	require.NoError(t, err)
	c1, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	svc.cache = &preparedReviewCache{openAIWSLatencyMemoryCache: cache, beforePoolRead: func() {
		require.NoError(t, first.ApplyUpstreamSessionIdentity(ctx, c1, &a, first.model))
		first.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
		require.Empty(t, svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey).Candidates)
	}}
	require.NoError(t, second.ApplyUpstreamSessionIdentity(ctx, c2, &a, second.model))
	require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c2))
}

func TestOpenAISessionPoolStateReadFailureCannotSilentlyChangeRoute(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	ctx := context.Background()
	session := preparedHTTPSession(svc, 1, "read-failure")
	_, err := session.PrepareHTTP(ctx, []byte("{\"input\":\"hello\"}"))
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, session.model))
	session.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	svc.cache = &preparedReviewCache{openAIWSLatencyMemoryCache: cache, failStateRead: true}
	require.Error(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, session.model),
		"无法确认原上游会话时应停止发送，不能静默换回客户端会话标识")
	require.Empty(t, OpenAIWSLatencyUpstreamSessionID(c))
	_, _, err = session.SelectTarget(ctx, nil, OpenAIUpstreamTransportHTTPSSE)
	require.ErrorIs(t, err, ErrOpenAIPreparedSessionUnavailable)
	next := preparedHTTPSession(svc, 1, "read-failure")
	_, err = next.PrepareHTTP(ctx, []byte("{\"input\":\"retry\"}"))
	require.ErrorIs(t, err, ErrOpenAIPreparedSessionUnavailable)
	require.ErrorIs(t, next.Prepare(ctx, []byte("{\"input\":\"retry\"}")), ErrOpenAIPreparedSessionUnavailable)
}

func TestOpenAISessionPoolCommittedSlowSwitchSurvivesPendingExpiry(t *testing.T) {
	for _, httpEntry := range []bool{true, false} {
		t.Run(map[bool]string{true: "http", false: "ws"}[httpEntry], func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			seedPreparedPool(t, svc, a, 1)
			ctx := context.Background()
			first := preparedHTTPSession(svc, 1, "committed-switch")
			svc.updateOpenAIWSLatencyState(ctx, first.key(first.model), func(state *openAIWSLatencyState) bool {
				*state = openAIWSLatencyState{AccountID: a.ID, ActiveUpstreamSessionID: "old-slow", UpstreamSessionID: "old-slow"}
				return true
			})
			require.True(t, first.assignPreparedCandidate(ctx, first.model, a.ID, false))
			first.setRecovery(true, true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			require.NoError(t, first.ApplyUpstreamSessionIdentity(ctx, c, &a, first.model))
			first.Observe(ctx, 1, &a, nil, errors.New("request failed"))
			svc.updateOpenAIWSLatencyState(ctx, first.key(first.model), func(state *openAIWSLatencyState) bool {
				state.PendingUntil = time.Now().Add(-time.Second).UnixMilli()
				return true
			})
			next := preparedHTTPSession(svc, 1, "committed-switch")
			payload := []byte("{\"input\":\"retry\",\"previous_response_id\":\"response-on-prepared\"}")
			if httpEntry {
				retry, err := next.PrepareHTTP(ctx, payload)
				require.NoError(t, err)
				require.False(t, retry)
			} else {
				require.NoError(t, next.Prepare(ctx, payload))
			}
			selection, _, err := next.SelectTarget(ctx, nil, OpenAIUpstreamTransportHTTPSSE)
			require.NoError(t, err)
			require.NotNil(t, selection)
			if selection.ReleaseFunc != nil {
				defer selection.ReleaseFunc()
			}
			require.NoError(t, next.BeforeRequest(ctx, 1, &a, payload, next.model))
			require.NoError(t, next.ApplyUpstreamSessionIdentity(ctx, c, &a, next.model))
			require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
		})
	}
}

func TestOpenAISessionPoolFailedFirstDecisionStopsBeforeSending(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	ctx := context.Background()
	session := preparedHTTPSession(svc, 1, "failed-first-write")
	svc.cache = &preparedReviewCache{openAIWSLatencyMemoryCache: cache, failStateWrite: true}
	_, err := session.PrepareHTTP(ctx, []byte("{\"input\":\"hello\"}"))
	require.ErrorIs(t, err, ErrOpenAIPreparedSessionUnavailable)
}

func TestOpenAISessionPoolLegacyBindingIsMigratedToOnlyOneThread(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	ctx := context.Background()
	group, key := int64(1), &APIKey{ID: 7}
	payload := []byte("{\"input\":\"hello\",\"model\":\"gpt-5.1\"}")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("session_id", "shared-parent")
	hash := svc.GenerateSessionHash(c, payload)
	old := preparedHTTPSession(svc, 1, hash)
	svc.updateOpenAIWSLatencyState(ctx, old.key(old.model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, InitialDecisionMade: true, UpstreamSessionID: "private-prepared", ActiveUpstreamSessionID: "private-prepared"}
		return true
	})
	// 更早版本残留的同一绑定也不能成为绕过新版迁移归属的入口。
	svc.updateOpenAIWSLatencyState(ctx, "1:"+old.scope+":"+hashOpenAIWSLatencyModel(old.model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, UpstreamSessionID: "private-prepared", ActiveUpstreamSessionID: "private-prepared"}
		return true
	})
	for _, thread := range []string{"thread-one", "thread-two"} {
		c.Request.Header.Set("thread-id", thread)
		next := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &group, key, hash, "gpt-5.1", "gpt-5.1", OpenAIEndpointCapabilityResponses, payload, c)
		if thread == "thread-one" {
			require.Equal(t, "private-prepared", next.state(ctx).ActiveUpstreamSessionID)
		} else {
			require.Empty(t, next.state(ctx).ActiveUpstreamSessionID, "旧的父级会话绑定不能重复迁移给另一线程")
		}
	}
}

func TestOpenAISessionPoolConcurrentLegacyMigrationHasOneOwner(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	ctx := context.Background()
	old := preparedHTTPSession(svc, 1, "legacy-parent")
	_, saved := svc.updateOpenAIWSLatencyState(ctx, old.key(old.model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, InitialDecisionMade: true, UpstreamSessionID: "private-prepared", ActiveUpstreamSessionID: "private-prepared"}
		return true
	})
	require.True(t, saved)
	group := int64(1)
	start := make(chan struct{})
	results := make(chan *OpenAIWSLatencySession, 2)
	for _, scope := range []string{"thread-one", "thread-two"} {
		go func(scope string) {
			<-start
			results <- newOpenAIWSLatencySession(ctx, svc, &group, &APIKey{ID: 7}, scope, "hash", old.model, old.upstreamModel, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportHTTPSSE, nil, old.scope)
		}(scope)
	}
	close(start)
	owners := 0
	var winner *OpenAIWSLatencySession
	for range 2 {
		session := <-results
		require.NoError(t, session.initializationErr)
		if session.state(ctx).ActiveUpstreamSessionID != "" {
			owners++
			winner = session
		}
	}
	require.Equal(t, 1, owners)
	// 模拟完成接管时整体替换状态，归属标记不能随业务字段一起丢失。
	_, saved = svc.updateOpenAIWSLatencyState(ctx, winner.key(winner.model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, InitialDecisionMade: true, ActiveUpstreamSessionID: "updated-private"}
		return true
	})
	require.True(t, saved)
	require.Equal(t, "updated-private", old.state(ctx).ActiveUpstreamSessionID)
	third := newOpenAIWSLatencySession(ctx, svc, &group, &APIKey{ID: 7}, "thread-three", "hash", old.model, old.upstreamModel, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportHTTPSSE, nil, old.scope)
	require.NoError(t, third.initializationErr)
	require.Empty(t, third.state(ctx).ActiveUpstreamSessionID)
}

func TestOpenAISessionPoolLegacyMigrationWriteFailureCanRetry(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	ctx := context.Background()
	old := preparedHTTPSession(svc, 1, "legacy-parent")
	svc.updateOpenAIWSLatencyState(ctx, old.key(old.model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, InitialDecisionMade: true, UpstreamSessionID: "private-prepared", ActiveUpstreamSessionID: "private-prepared"}
		return true
	})
	group := int64(1)
	newSession := func(scope string) *OpenAIWSLatencySession {
		return newOpenAIWSLatencySession(ctx, svc, &group, &APIKey{ID: 7}, scope, "hash", old.model, old.upstreamModel, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportHTTPSSE, nil, old.scope)
	}
	fault := &preparedReviewCache{openAIWSLatencyMemoryCache: cache, failWriteKey: "conversation:v2:1:thread-one"}
	svc.cache = fault
	first := newSession("thread-one")
	require.ErrorIs(t, first.initializationErr, ErrOpenAIPreparedSessionUnavailable)
	require.Empty(t, newSession("thread-two").state(ctx).ActiveUpstreamSessionID)
	fault.failWriteKey = ""
	retry := newSession("thread-one")
	require.NoError(t, retry.initializationErr)
	require.Equal(t, "private-prepared", retry.state(ctx).ActiveUpstreamSessionID)
}

func TestOpenAISessionPoolCommittedToolContinuationAfterCompletionWriteFailure(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 1)
	ctx := context.Background()
	first := preparedHTTPSession(svc, 1, "tool-continuation")
	_, err := first.PrepareHTTP(ctx, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, first.ApplyUpstreamSessionIdentity(ctx, c, &a, first.model))

	// 上游成功产生工具调用，但本地接管成功状态未能写入。
	svc.cache = &preparedReviewCache{openAIWSLatencyMemoryCache: cache, failStateWrite: true}
	first.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	svc.cache = cache
	require.True(t, first.state(ctx).CandidateCommitted)

	group := int64(1)
	payload := []byte(`{"previous_response_id":"resp-on-prepared","input":[{"type":"function_call_output","call_id":"call-1","output":"ok"}]}`)
	next := newOpenAIWSLatencySession(ctx, svc, &group, &APIKey{ID: 7}, first.scope, first.sessionHash, first.model, first.upstreamModel, first.requiredCapability, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, payload)
	require.NoError(t, next.Prepare(ctx, payload))
	require.NoError(t, next.ApplyUpstreamSessionIdentity(ctx, c, &a, next.model))
	require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
	next.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)

	// 长连接首包不执行 BeforeRequest；同一连接下一次工具回传会执行。
	followup := []byte(`{"previous_response_id":"resp-on-prepared-2","input":[{"type":"function_call_output","call_id":"call-2","output":"ok"}]}`)
	require.NoError(t, next.BeforeRequest(ctx, 2, &a, followup, next.model))
	require.NoError(t, next.ApplyUpstreamSessionIdentity(ctx, c, &a, next.model, 2))
	require.Equal(t, "prepared-11-0", OpenAIWSLatencyUpstreamSessionID(c))
	require.True(t, next.state(ctx).CandidateCommitted, "工具回传不冒充用户请求确认接管成功")
}

func TestOpenAISessionPoolFailoverWriteFailureStopsBeforeSending(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	a, b := preparedTestAccount(11, 1), preparedTestAccount(22, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}
	seedPreparedPool(t, svc, a, 1)
	ctx := context.Background()
	session := preparedHTTPSession(svc, 1, "failover-write-failure")
	_, err := session.PrepareHTTP(ctx, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, session.model))
	session.Observe(ctx, 1, &a, &OpenAIForwardResult{}, nil)
	before := session.state(ctx)

	svc.cache = &preparedReviewCache{openAIWSLatencyMemoryCache: cache, failStateWrite: true}
	require.ErrorIs(t, session.ApplyUpstreamSessionIdentity(ctx, c, &b, session.model), ErrOpenAIPreparedSessionUnavailable)
	require.Empty(t, OpenAIWSLatencyUpstreamSessionID(c))
	require.Equal(t, before, session.state(ctx), "写入失败应保留原线路供重试")

	svc.cache = cache
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &b, session.model))
	require.Equal(t, b.ID, session.state(ctx).AccountID)
	require.Empty(t, session.state(ctx).ActiveUpstreamSessionID)
	// 普通线路已保存时，重复请求不依赖一次不必要的写入。
	svc.cache = &preparedReviewCache{openAIWSLatencyMemoryCache: cache, failStateWrite: true}
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &b, session.model))
}

func TestOpenAISessionPoolFailoverRechecksConcurrentRoute(t *testing.T) {
	for _, sameRoute := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflicting_route", true: "same_route"}[sameRoute], func(t *testing.T) {
			svc, cache := newOpenAIWSLatencyTestService(t)
			ctx := context.Background()
			session := preparedHTTPSession(svc, 1, "concurrent-failover")
			_, saved := svc.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool {
				*state = openAIWSLatencyState{AccountID: 11, InitialDecisionMade: true, ActiveUpstreamSessionID: "old-prepared", UpstreamSessionID: "old-prepared"}
				return true
			})
			require.True(t, saved)
			winner := []byte(`{"account_id":33,"initial_decision_made":true,"active_upstream_session_id":"new-private","upstream_session_id":"new-private"}`)
			if sameRoute {
				winner = []byte(`{"account_id":22,"initial_decision_made":true}`)
			}
			svc.cache = &preparedReviewCache{openAIWSLatencyMemoryCache: cache, beforeStateWrite: func() {
				cache.mu.Lock()
				cache.states[session.key(session.model)] = winner
				cache.mu.Unlock()
			}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			b := preparedTestAccount(22, 1)
			err := session.ApplyUpstreamSessionIdentity(ctx, c, &b, session.model)
			if sameRoute {
				require.NoError(t, err, "另一个请求已保存同一普通线路时可继续")
			} else {
				require.ErrorIs(t, err, ErrOpenAIPreparedSessionUnavailable)
				require.Equal(t, int64(33), session.state(ctx).AccountID, "不能覆盖并发请求已确认的线路")
			}
		})
	}
}

// 用可推进的时钟验证真实过期行为，无需让测试等待两小时。
type preparedExpiringCache struct {
	*openAIWSLatencyMemoryCache
	now       time.Time
	expiresAt map[string]time.Time
}

func (cache *preparedExpiringCache) GetOpenAIWSLatencyState(ctx context.Context, key string) ([]byte, error) {
	if deadline, exists := cache.expiresAt[key]; exists && !cache.now.Before(deadline) {
		cache.mu.Lock()
		delete(cache.states, key)
		cache.mu.Unlock()
		delete(cache.expiresAt, key)
	}
	return cache.openAIWSLatencyMemoryCache.GetOpenAIWSLatencyState(ctx, key)
}

func (cache *preparedExpiringCache) CompareAndSwapOpenAIWSLatencyState(ctx context.Context, key string, expected, next []byte, ttl time.Duration) (bool, error) {
	if _, err := cache.GetOpenAIWSLatencyState(ctx, key); err != nil {
		return false, err
	}
	updated, err := cache.openAIWSLatencyMemoryCache.CompareAndSwapOpenAIWSLatencyState(ctx, key, expected, next, ttl)
	if updated {
		cache.expiresAt[key] = cache.now.Add(ttl)
	}
	return updated, err
}

func TestOpenAISessionPoolMigratedBindingRefreshesAliasLifetime(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "committed"}[committed], func(t *testing.T) {
			svc, memory := newOpenAIWSLatencyTestService(t)
			cache := &preparedExpiringCache{openAIWSLatencyMemoryCache: memory, now: time.Now(), expiresAt: make(map[string]time.Time)}
			svc.cache = cache
			ctx := context.Background()
			a := preparedTestAccount(11, 1)
			group, model, scope := int64(1), "gpt-5.1", "migrated-thread"
			legacyKey := "1:" + scope + ":" + hashOpenAIWSLatencyModel(model)
			_, saved := svc.updateOpenAIWSLatencyState(ctx, legacyKey, func(state *openAIWSLatencyState) bool {
				*state = openAIWSLatencyState{AccountID: a.ID, InitialDecisionMade: true, ActiveUpstreamSessionID: "private-prepared", UpstreamSessionID: "private-prepared"}
				if committed {
					state.ActiveUpstreamSessionID = "old-slow"
					state.TargetID, state.TargetResultID, state.CandidateLeaseID = a.ID, "result", "lease"
					state.CandidateCommitted = true
				}
				return true
			})
			require.True(t, saved)
			session := newOpenAIWSLatencySession(ctx, svc, &group, &APIKey{ID: 7}, scope, "parent", model, model, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, nil)
			require.NoError(t, session.initializationErr)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())

			cache.now = cache.now.Add(time.Hour)
			require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, model))
			require.Equal(t, "private-prepared", OpenAIWSLatencyUpstreamSessionID(c))
			cache.now = cache.now.Add(time.Hour + time.Second)
			require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, model))
			require.Equal(t, "private-prepared", OpenAIWSLatencyUpstreamSessionID(c), "持续活跃的迁移绑定超过原有效期仍应可用")

			// 活跃长连接更换模型后，新请求也能找到同一入口。
			next := newOpenAIWSLatencySession(ctx, svc, &group, &APIKey{ID: 7}, scope, "parent", "gpt-other", "gpt-other", OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportHTTPSSE, nil)
			require.NoError(t, next.initializationErr)
			require.Equal(t, "private-prepared", next.state(ctx).UpstreamSessionID)
		})
	}
}

func TestOpenAISessionPoolMigratedAliasRefreshFailureStopsBeforeSending(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	ctx := context.Background()
	group, model, scope := int64(1), "gpt-5.1", "alias-refresh-failure"
	a := preparedTestAccount(11, 1)
	_, saved := svc.updateOpenAIWSLatencyState(ctx, "1:"+scope+":"+hashOpenAIWSLatencyModel(model), func(state *openAIWSLatencyState) bool {
		// 兼容尚未包含 ActiveUpstreamSessionID 的旧版绑定。
		*state = openAIWSLatencyState{AccountID: a.ID, UpstreamSessionID: "legacy-private"}
		return true
	})
	require.True(t, saved)
	session := newOpenAIWSLatencySession(ctx, svc, &group, &APIKey{ID: 7}, scope, "parent", model, model, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportHTTPSSE, nil)
	require.NoError(t, session.initializationErr)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	fault := &preparedReviewCache{openAIWSLatencyMemoryCache: cache, failWriteKey: session.key(model)}
	svc.cache = fault
	require.ErrorIs(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, model), ErrOpenAIPreparedSessionUnavailable)
	require.Empty(t, OpenAIWSLatencyUpstreamSessionID(c))

	fault.failWriteKey = ""
	require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &a, model))
	require.Equal(t, "legacy-private", OpenAIWSLatencyUpstreamSessionID(c))
}
