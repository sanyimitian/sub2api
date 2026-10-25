package service

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAISessionPoolAccountRetryIsIndependentAndShared(t *testing.T) {
	for _, seconds := range []int{30, 31, 1800, 2700, 86400} {
		t.Run(fmt.Sprintf("%d_seconds", seconds), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				svc, cache := newOpenAIWSLatencyTestService(t)
				svc.cfg.Gateway.OpenAIWS.Enabled = false
				a, b := preparedTestAccount(382, 1), preparedTestAccount(385, 1)
				svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
				upstream := &preparedProbeUpstream{fail: true}
				svc.httpUpstream = upstream
				gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 6, sessionPoolRetryIntervalSeconds: seconds,
					latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2}, expiresAt: time.Now().Add(48 * time.Hour).UnixNano()})
				ctx := context.Background()
				started := time.Now()
				svc.refillOpenAISessionPool(ctx)
				pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
				aRetry := pool.AccountProbes[a.ID].RetryAfter
				require.Equal(t, started.Add(time.Duration(seconds)*time.Second).UnixMilli(), aRetry)
				require.EqualValues(t, 10, upstream.calls.Load())

				// 新账号在另一实例加入，不继承旧账号的等待；旧实例写入的状态仍保留。
				time.Sleep(15 * time.Second)
				peer := &OpenAIGatewayService{cfg: svc.cfg, cache: cache, settingService: svc.settingService,
					accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}, httpUpstream: upstream}
				peer.refillOpenAISessionPool(ctx)
				pool = peer.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
				bRetry := pool.AccountProbes[b.ID].RetryAfter
				require.Equal(t, aRetry, pool.AccountProbes[a.ID].RetryAfter)
				require.Equal(t, started.Add(time.Duration(seconds)*time.Second+15*time.Second).UnixMilli(), bRetry)
				require.EqualValues(t, 20, upstream.calls.Load(), "只探测新增账号，旧账号仍在独立等待")
				peer.refillOpenAISessionPool(ctx)
				require.EqualValues(t, 20, upstream.calls.Load())

				// A 到期时 B 仍需等十五秒；B 不阻挡 A，也不被 A 的新等待时间覆盖。
				time.Sleep(time.Duration(seconds)*time.Second - 15*time.Second)
				peer.refillOpenAISessionPool(ctx)
				pool = peer.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
				require.EqualValues(t, 30, upstream.calls.Load())
				require.Equal(t, started.Add(2*time.Duration(seconds)*time.Second).UnixMilli(), pool.AccountProbes[a.ID].RetryAfter)
				require.Equal(t, bRetry, pool.AccountProbes[b.ID].RetryAfter)
			})
		})
	}
}

func TestOpenAISessionPoolAccountCountsOnlyItsOwnRounds(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Gateway.OpenAIWS.Enabled = false
	accounts := []Account{preparedTestAccount(11, 1), preparedTestAccount(22, 1), preparedTestAccount(33, 1)}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts}
	u := &preparedProbeUpstream{fail: true}
	svc.httpUpstream = u
	// 一个空位时每轮只探测一路，即使账号有多个模型或协议，也只消耗它自己的一轮。
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 1,
		latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	ctx := context.Background()
	svc.refillOpenAISessionPool(ctx)
	pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	rounds := 0
	for _, account := range accounts {
		state := pool.AccountProbes[account.ID]
		require.Zero(t, state.RetryAfter, "没有被安排满五轮的账号不能提前进入等待")
		rounds += state.Rounds
	}
	require.Equal(t, 5, rounds)
	for i := 0; i < 2; i++ {
		svc.refillOpenAISessionPool(ctx)
	}
	pool = svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	require.EqualValues(t, 15, u.calls.Load())
	for _, account := range accounts {
		require.Greater(t, pool.AccountProbes[account.ID].RetryAfter, time.Now().UnixMilli())
	}
	svc.refillOpenAISessionPool(ctx)
	require.EqualValues(t, 15, u.calls.Load())
}

func TestOpenAISessionPoolAccountBackoffPreservesCandidatesAndModelQuotas(t *testing.T) {
	now := time.Now().UnixMilli()
	a, b := preparedTestAccount(11, 1), preparedTestAccount(22, 1)
	routes := []openAISessionPoolRoute{
		{account: &a, model: "model-a", upstreamModel: "model-a", fingerprint: openAISessionPoolAccountFingerprint(&a)},
		{account: &b, model: "model-b", upstreamModel: "model-b", fingerprint: openAISessionPoolAccountFingerprint(&b)},
	}
	pool := openAIWSLatencyCandidatePool{AccountProbes: map[int64]openAISessionPoolAccountProbeState{a.ID: {RetryAfter: now + 1800000}},
		Candidates: []openAIWSLatencyReusableCandidate{{AccountID: a.ID, ProbeModel: "model-a", UpstreamModel: "model-a",
			UpstreamSessionID: "keep-me", AccountFingerprint: routes[0].fingerprint, FirstTokenMS: 100, ExpiresAt: now + 60000}}}
	require.Equal(t, 1, trimOpenAISessionPool(&pool, routes, now, 6))
	planned := planOpenAISessionPoolProbes(&pool, routes, now, 6, 6, map[string]int{})
	require.Len(t, planned, 3, "冷却模型仍保留原配额，其他模型不能挤占")
	for _, route := range planned {
		require.Equal(t, b.ID, route.account.ID)
	}
	require.Len(t, pool.Candidates, 1, "冷却不删除合格候选")
}

func TestOpenAISessionPoolIgnoresLegacyGlobalBackoff(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Gateway.OpenAIWS.Enabled = false
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{preparedTestAccount(385, 1)}}
	u := &preparedProbeUpstream{}
	svc.httpUpstream = u
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 1,
		latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2, ProbeReuseSeconds: 600}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	ctx := context.Background()
	svc.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		pool.RetryAfter = time.Now().Add(30 * time.Minute).UnixMilli()
		return true
	})
	svc.refillOpenAISessionPool(ctx)
	pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	require.Zero(t, pool.RetryAfter)
	require.Len(t, pool.Candidates, 1)
	require.EqualValues(t, 1, u.calls.Load())
}

func TestOpenAISessionPoolTightenedThresholdAppliesToSnapshotAndClaim(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Gateway.OpenAIWS.Enabled = false
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	settings := &cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 1,
		sessionPoolFirstTokenTimeoutSeconds: 20, expiresAt: time.Now().Add(time.Hour).UnixNano()}
	gatewayForwardingCache.Store(settings)
	seedPreparedPool(t, svc, a, 1)
	ctx := context.Background()
	svc.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		pool.Candidates[0].FirstTokenMS = 18000
		return true
	})
	snapshot, err := svc.GetOpenAISessionPoolSnapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snapshot.Candidates, 1)
	tightened := *settings
	tightened.sessionPoolFirstTokenTimeoutSeconds = 5
	gatewayForwardingCache.Store(&tightened)
	snapshot, err = svc.GetOpenAISessionPoolSnapshot(ctx)
	require.NoError(t, err)
	require.Empty(t, snapshot.Candidates)
	_, _, claimed := preparedHTTPSession(svc, 1, "tightened-threshold-user").reservePreparedCandidate(ctx, "gpt-5.1")
	require.False(t, claimed, "收紧门槛之后不能领取旧的不合格候选")
}

func TestOpenAISessionPoolRetrySettingPreservesExistingWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc, _ := newOpenAIWSLatencyTestService(t)
		svc.cfg.Gateway.OpenAIWS.Enabled = false
		a := preparedTestAccount(384, 1)
		svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
		upstream := &preparedProbeUpstream{fail: true}
		svc.httpUpstream = upstream
		settings := &cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 6,
			sessionPoolRetryIntervalSeconds: 1800, latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2},
			expiresAt: time.Now().Add(time.Hour).UnixNano()}
		gatewayForwardingCache.Store(settings)
		ctx := context.Background()
		started := time.Now()
		svc.refillOpenAISessionPool(ctx)

		changed := *settings
		changed.sessionPoolRetryIntervalSeconds = 30
		gatewayForwardingCache.Store(&changed)
		time.Sleep(time.Minute)
		svc.refillOpenAISessionPool(ctx)
		pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
		require.EqualValues(t, 10, upstream.calls.Load(), "修改配置不提前结束已有等待")
		require.Equal(t, started.Add(30*time.Minute).UnixMilli(), pool.AccountProbes[a.ID].RetryAfter)

		time.Sleep(29 * time.Minute)
		svc.refillOpenAISessionPool(ctx)
		pool = svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
		require.EqualValues(t, 20, upstream.calls.Load())
		require.Equal(t, started.Add(30*time.Minute+30*time.Second).UnixMilli(), pool.AccountProbes[a.ID].RetryAfter, "新等待使用修改后的配置")
	})
}

func TestOpenAISessionPoolLongRetrySurvivesCacheExpiryAndRestart(t *testing.T) {
	svc, memory := newOpenAIWSLatencyTestService(t)
	started := time.Now()
	cache := &preparedExpiringCache{openAIWSLatencyMemoryCache: memory, now: started, expiresAt: make(map[string]time.Time)}
	svc.cache = cache
	ctx := context.Background()
	retryAfter := started.Add(24 * time.Hour).UnixMilli()
	_, saved := svc.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		pool.AccountProbes = map[int64]openAISessionPoolAccountProbeState{384: {RetryAfter: retryAfter}}
		return true
	})
	require.True(t, saved)
	require.Greater(t, cache.expiresAt[openAISessionPoolKey].UnixMilli(), retryAfter, "缓存必须覆盖完整等待期")
	cache.now = started.Add(23 * time.Hour)
	peer := &OpenAIGatewayService{cache: cache}
	pool := peer.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
	require.Equal(t, retryAfter, pool.AccountProbes[384].RetryAfter, "重启后不能因旧两小时缓存期限丢失等待")
}
