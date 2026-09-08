package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorRequestLedgerTracksAttemptsAndFailurePrecedence(t *testing.T) {
	mr := miniredis.RunT(t)
	store := NewChannelMonitorRequestLedgerStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	ctx := context.Background()
	ttl := time.Minute

	first, err := store.RegisterAttempt(ctx, 7, "request-1", 11, 4, ttl)
	require.NoError(t, err)
	require.Equal(t, int64(11), first.AccountID)
	require.Equal(t, int64(4), first.Generation)
	second, err := store.RegisterAttempt(ctx, 7, "request-1", 11, 99, ttl)
	require.NoError(t, err)
	require.Equal(t, first.Generation, second.Generation, "重复登记不得覆盖观测代次")

	require.NoError(t, store.RecordTransport(ctx, 7, "request-1", 11, service.ChannelMonitorTransportSucceeded, 1))
	require.NoError(t, store.RecordTransport(ctx, 7, "request-1", 11, service.ChannelMonitorTransportFailed, 2))
	require.NoError(t, store.RecordTransport(ctx, 7, "request-1", 11, service.ChannelMonitorTransportSucceeded, 1))

	ledger, err := store.Read(ctx, 7, "request-1")
	require.NoError(t, err)
	require.Len(t, ledger.Attempts, 1)
	require.Equal(t, service.ChannelMonitorTransportFailed, ledger.Attempts[0].TransportState)
	require.Equal(t, 2, ledger.Attempts[0].DurationSeconds)
}

func TestChannelMonitorRequestLedgerSeparatesModelsAndExpires(t *testing.T) {
	mr := miniredis.RunT(t)
	store := NewChannelMonitorRequestLedgerStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	ctx := context.Background()
	require.Error(t, store.RecordTransport(ctx, 9, "request-extra", 22, service.ChannelMonitorTransportSucceeded, 1))
	_, err := store.Read(ctx, 9, "request-extra")
	require.Error(t, err, "未登记的账号终态不得隐式创建账本")
	require.NoError(t, func() error {
		_, err := store.RegisterAttempt(ctx, 9, "request-extra", 22, 1, time.Second)
		return err
	}())
	require.NoError(t, store.RecordTransport(ctx, 9, "request-extra", 22, service.ChannelMonitorTransportSucceeded, 1))
	_, err = store.Read(ctx, 9, "request-primary")
	require.Error(t, err, "主模型和附加模型账本必须隔离")
	mr.FastForward(service.ChannelMonitorRequestLedgerTTL + time.Second)
	_, err = store.Read(ctx, 9, "request-extra")
	require.Error(t, err, "账本必须通过 TTL 清理")
}

func TestChannelMonitorRequestLedgerCompletionIsIdempotent(t *testing.T) {
	mr := miniredis.RunT(t)
	store := NewChannelMonitorRequestLedgerStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	ctx := context.Background()
	require.NoError(t, func() error {
		_, err := store.RegisterAttempt(ctx, 3, "request", 31, 2, time.Minute)
		return err
	}())
	require.NoError(t, store.RecordTransport(ctx, 3, "request", 31, service.ChannelMonitorTransportFailed, 1))

	var wg sync.WaitGroup
	claimed := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.Finalize(ctx, 3, "request", service.MonitorStatusFailed, time.Now().UTC(), []int{2, 5, 30, 60, 120})
			require.NoError(t, err)
			claimed <- result.Claimed
		}()
	}
	wg.Wait()
	close(claimed)
	wins := 0
	for won := range claimed {
		if won {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	ledger, err := store.Read(ctx, 3, "request")
	require.NoError(t, err)
	require.True(t, ledger.Completed)
	require.Equal(t, service.MonitorStatusFailed, ledger.FinalStatus)
}

func TestChannelMonitorRequestLedgerFinalizesFailureAndOperationalRulesAtomically(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewChannelMonitorRequestLedgerStore(rdb)
	cooldown := NewChannelMonitorCooldownStore(rdb)
	ctx := context.Background()
	now := time.Unix(2000, 0).UTC()
	ladder := []int{2, 5, 30, 60, 120}

	_, err := store.RegisterAttempt(ctx, 10, "failed-request", 101, 0, time.Minute)
	require.NoError(t, err)
	_, err = store.RegisterAttempt(ctx, 10, "failed-request", 102, 0, time.Minute)
	require.NoError(t, err)
	require.NoError(t, store.RecordTransport(ctx, 10, "failed-request", 101, service.ChannelMonitorTransportFailed, 2))
	require.NoError(t, store.RecordTransport(ctx, 10, "failed-request", 102, service.ChannelMonitorTransportSucceeded, 3))
	result, err := store.Finalize(ctx, 10, "failed-request", service.MonitorStatusFailed, now, ladder)
	require.NoError(t, err)
	require.True(t, result.Claimed)
	for _, accountID := range []int64{101, 102} {
		cooling, currentErr := cooldown.IsCooling(ctx, accountID, now.Add(time.Minute))
		require.NoError(t, currentErr)
		require.True(t, cooling)
	}

	existing, err := cooldown.ObserveFailure(ctx, 202, now, ladder)
	require.NoError(t, err)
	for _, attempt := range []struct {
		id         int64
		generation int64
		state      service.ChannelMonitorTransportState
	}{
		{id: 201, state: service.ChannelMonitorTransportFailed},
		{id: 202, generation: existing.Generation, state: service.ChannelMonitorTransportSucceeded},
		{id: 203, state: service.ChannelMonitorTransportUnknown},
	} {
		_, err = store.RegisterAttempt(ctx, 10, "operational-request", attempt.id, attempt.generation, time.Minute)
		require.NoError(t, err)
		require.NoError(t, store.RecordTransport(ctx, 10, "operational-request", attempt.id, attempt.state, 1))
	}
	result, err = store.Finalize(ctx, 10, "operational-request", service.MonitorStatusOperational, now.Add(time.Second), ladder)
	require.NoError(t, err)
	require.True(t, result.Claimed)
	failedCooling, err := cooldown.IsCooling(ctx, 201, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, failedCooling)
	successCurrent, err := cooldown.(*channelMonitorCooldownStore).Current(ctx, 202)
	require.NoError(t, err)
	require.Equal(t, 0, successCurrent.Streak)
	unknownCooling, err := cooldown.IsCooling(ctx, 203, now.Add(time.Minute))
	require.NoError(t, err)
	require.False(t, unknownCooling)
}

func TestChannelMonitorRequestLedgerRejectsIncompleteWithoutCooling(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewChannelMonitorRequestLedgerStore(rdb)
	cooldown := NewChannelMonitorCooldownStore(rdb)
	ctx := context.Background()
	_, err := store.RegisterAttempt(ctx, 11, "incomplete", 301, 0, time.Minute)
	require.NoError(t, err)
	_, err = store.Finalize(ctx, 11, "incomplete", service.MonitorStatusFailed, time.Now().UTC(), []int{2, 5, 30, 60, 120})
	require.Error(t, err)
	cooling, err := cooldown.IsCooling(ctx, 301, time.Now().UTC())
	require.NoError(t, err)
	require.False(t, cooling)
}
