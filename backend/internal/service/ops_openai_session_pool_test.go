package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAISessionPoolSnapshotOnlyExposesAvailableSafeMetadata(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	a.Name = "账号甲"
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	seedPreparedPool(t, svc, a, 8)
	svc.updateOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		pool.Candidates[0].ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
		pool.Candidates[1].LeaseID, pool.Candidates[1].LeaseUntil = "private-lease", time.Now().Add(time.Minute).UnixMilli()
		pool.Candidates[2].Committed = true
		pool.Candidates[3].AccountFingerprint = "private-stale-fingerprint"
		pool.Candidates[4].FirstTokenMS = 10000
		pool.Candidates[5].UpstreamSessionID = ""
		pool.Candidates[6].UpstreamModel = "unconfigured"
		return true
	})
	before, err := cache.GetOpenAIWSLatencyState(context.Background(), openAISessionPoolKey)
	require.NoError(t, err)
	snapshot, err := (&OpsService{openAIGatewayService: svc}).GetOpenAISessionPoolSnapshot(context.Background())
	require.NoError(t, err)
	require.True(t, snapshot.Enabled)
	require.Len(t, snapshot.Candidates, 1)
	candidate := snapshot.Candidates[0]
	require.EqualValues(t, 11, candidate.AccountID)
	require.Equal(t, "账号甲", candidate.AccountName)
	require.Equal(t, "gpt-5.1", candidate.Model)
	require.True(t, candidate.ExpiresAt.After(snapshot.ObservedAt))
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	for _, secret := range []string{"upstream_session_id", "account_fingerprint", "lease_id", "prepared-11-", "test-only"} {
		require.NotContains(t, string(encoded), secret)
	}
	after, err := cache.GetOpenAIWSLatencyState(context.Background(), openAISessionPoolKey)
	require.NoError(t, err)
	require.Equal(t, before, after, "查看页面不能改变会话池和租约")
}

type failingSessionPoolSnapshotCache struct{ *openAIWSLatencyMemoryCache }

func (c failingSessionPoolSnapshotCache) GetOpenAIWSLatencyState(context.Context, string) ([]byte, error) {
	return nil, errors.New("cache unavailable")
}

func TestOpenAISessionPoolSnapshotReportsCacheFailure(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	svc.cache = failingSessionPoolSnapshotCache{cache}
	_, err := svc.GetOpenAISessionPoolSnapshot(context.Background())
	require.Error(t, err)
}

func TestOpenAISessionPoolSnapshotDisabledAndEmpty(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	snapshot, err := svc.GetOpenAISessionPoolSnapshot(context.Background())
	require.NoError(t, err)
	require.Empty(t, snapshot.Candidates)
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{expiresAt: time.Now().Add(time.Hour).UnixNano()})
	svc.cache = nil
	snapshot, err = svc.GetOpenAISessionPoolSnapshot(context.Background())
	require.NoError(t, err)
	require.False(t, snapshot.Enabled)
	require.NotNil(t, snapshot.Candidates)
}
