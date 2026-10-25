package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAISessionPoolBalancesSelectedModels(t *testing.T) {
	for _, concurrency := range []int{2, 6} {
		t.Run(fmt.Sprintf("concurrency=%d", concurrency), func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			svc.cfg.Gateway.OpenAIWS.Enabled = false
			a, b := preparedTestAccount(11, 1), preparedTestAccount(22, 1)
			a.Extra[OpenAISessionPoolModelsExtraKey] = []string{"gpt-6-astra", "gpt-6-sol"}
			b.Extra[OpenAISessionPoolModelsExtraKey] = []string{"gpt-6.1-sol"}
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}
			upstream := &preparedProbeUpstream{}
			svc.httpUpstream = upstream
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 6,
				latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: concurrency, ProbeReuseSeconds: 600}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
			ctx := context.Background()
			want := map[string]int{"gpt-6-astra": 2, "gpt-6-sol": 2, "gpt-6.1-sol": 2}
			svc.refillOpenAISessionPool(ctx)
			pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
			require.Equal(t, want, openAISessionPoolModelCounts(&pool, time.Now().UnixMilli()))
			require.EqualValues(t, 6, upstream.calls.Load())
			// 消费某个模型的全部候选，只补这个模型的缺口。
			svc.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
				for i := range pool.Candidates {
					if pool.Candidates[i].ProbeModel == "gpt-6-sol" {
						pool.Candidates[i].Committed = true
					}
				}
				return true
			})
			svc.refillOpenAISessionPool(ctx)
			pool = svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
			require.Equal(t, want, openAISessionPoolModelCounts(&pool, time.Now().UnixMilli()))
			require.Len(t, pool.Candidates, 8, "已独占的会话不被均分裁剪")
			require.EqualValues(t, 8, upstream.calls.Load())
		})
	}
}

func TestOpenAISessionPoolModelBalanceDoesNotFillFailedModelWithOthers(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	svc.cfg.Gateway.OpenAIWS.Enabled = false
	a := preparedTestAccount(11, 1)
	a.Extra[OpenAISessionPoolModelsExtraKey] = []string{"gpt-6-astra", "gpt-6-sol", "gpt-6.1-sol"}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	upstream := &preparedProbeUpstream{failModel: "gpt-6-astra"}
	svc.httpUpstream = upstream
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 6,
		latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2, ProbeReuseSeconds: 600}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	svc.refillOpenAISessionPool(context.Background())
	pool := svc.getOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey)
	require.Equal(t, map[string]int{"gpt-6-sol": 2, "gpt-6.1-sol": 2}, openAISessionPoolModelCounts(&pool, time.Now().UnixMilli()))
	require.Greater(t, pool.AccountProbes[a.ID].RetryAfter, time.Now().UnixMilli())
}

func TestOpenAISessionPoolModelBalanceTrimsSkewAndPreservesOwnership(t *testing.T) {
	now := time.Now().UnixMilli()
	a := preparedTestAccount(11, 1)
	fingerprint := openAISessionPoolAccountFingerprint(&a)
	routes := []openAISessionPoolRoute{}
	for _, model := range []string{"model-a", "model-b", "model-c"} {
		routes = append(routes, openAISessionPoolRoute{account: &a, model: model, upstreamModel: model, transport: OpenAIUpstreamTransportHTTPSSE, fingerprint: fingerprint})
	}
	pool := openAIWSLatencyCandidatePool{}
	for i := 0; i < 8; i++ {
		pool.Candidates = append(pool.Candidates, openAIWSLatencyReusableCandidate{AccountID: a.ID, ProbeModel: "model-a", UpstreamModel: "model-a",
			UpstreamSessionID: fmt.Sprintf("session-%d", i), AccountFingerprint: fingerprint, Transport: OpenAIUpstreamTransportHTTPSSE,
			FirstTokenMS: 100 + i, ExpiresAt: now + 60000})
	}
	pool.Candidates[6].LeaseID, pool.Candidates[6].LeaseUntil = "owned", now+60000
	pool.Candidates[7].Committed = true
	require.Equal(t, 2, trimOpenAISessionPool(&pool, routes, now, 6), "总数已经满但分布不均时，也腾出其他模型的名额")
	require.Len(t, pool.Candidates, 4)
	require.Equal(t, "session-0", pool.Candidates[0].UpstreamSessionID)
	require.Equal(t, "session-1", pool.Candidates[1].UpstreamSessionID)
	require.Equal(t, "owned", pool.Candidates[2].LeaseID)
	require.True(t, pool.Candidates[3].Committed)
	planned := planOpenAISessionPoolProbes(&pool, routes, now, 6, 6, map[string]int{})
	counts := map[string]int{}
	for _, route := range planned {
		counts[route.model]++
	}
	require.Equal(t, map[string]int{"model-b": 2, "model-c": 2}, counts)
}

func TestOpenAISessionPoolModelBalanceRemainderAndDuplicateRoutes(t *testing.T) {
	for _, target := range []int{1, 2, 5, 6, 7} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			a := preparedTestAccount(11, 1)
			routes := []openAISessionPoolRoute{{account: &a, model: "model-a"}, {account: &a, model: "model-a"},
				{account: &a, model: "model-a"}, {account: &a, model: "model-b"}, {account: &a, model: "model-c"}}
			pool := openAIWSLatencyCandidatePool{}
			plan := planOpenAISessionPoolProbes(&pool, routes, time.Now().UnixMilli(), target, 100, map[string]int{})
			require.Len(t, plan, target)
			counts := map[string]int{"model-a": 0, "model-b": 0, "model-c": 0}
			for _, route := range plan {
				counts[route.model]++
			}
			for _, count := range counts {
				require.GreaterOrEqual(t, count, target/3)
				require.LessOrEqual(t, count, (target+2)/3)
			}
			// 已存在的余数名额不会因后台再次检查而被来回替换。
			quotas := openAISessionPoolModelTargets(routes, map[string]int{"model-c": target/3 + 1}, target)
			if target%3 != 0 {
				require.Equal(t, target/3+1, quotas["model-c"])
			}
		})
	}
}
