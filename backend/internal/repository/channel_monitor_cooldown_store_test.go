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

func TestChannelMonitorCooldownStoreMergesConcurrentFailures(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewChannelMonitorCooldownStore(rdb)
	now := time.Unix(1000, 0).UTC()
	ladder := []int{2, 5, 30, 60, 120}
	var wg sync.WaitGroup
	events := make(chan int, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err := store.ObserveFailure(context.Background(), 77, now, ladder)
			require.NoError(t, err)
			events <- e.Streak
		}()
	}
	wg.Wait()
	close(events)
	for streak := range events {
		require.Equal(t, 1, streak)
	}
	active, err := store.IsCooling(context.Background(), 77, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, active)
}

func TestChannelMonitorCooldownStoreAdvancesAfterExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewChannelMonitorCooldownStore(rdb)
	now := time.Unix(1000, 0).UTC()
	ladder := []int{2, 5, 30, 60, 120}
	e1, err := store.ObserveFailure(context.Background(), 3, now, ladder)
	require.NoError(t, err)
	e2, err := store.ObserveFailure(context.Background(), 3, e1.Until.Add(time.Second), ladder)
	require.NoError(t, err)
	require.Equal(t, 2, e2.Streak)
	require.Equal(t, 5*time.Minute, e2.Until.Sub(e1.Until.Add(time.Second)))
	// A success from the old generation cannot delete the new event.
	require.NoError(t, store.ObserveSuccess(context.Background(), 3, e1.Generation, e2.Until.Add(time.Second)))
	active, err := store.IsCooling(context.Background(), 3, e2.Until)
	require.NoError(t, err)
	require.False(t, active)
}

func TestChannelMonitorCooldownStoreZeroGenerationSuccessCannotClearFailure(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewChannelMonitorCooldownStore(rdb)
	now := time.Unix(1000, 0).UTC()
	event, err := store.ObserveFailure(context.Background(), 13, now, []int{2, 5, 30, 60, 120})
	require.NoError(t, err)
	require.NoError(t, store.ObserveSuccess(context.Background(), 13, 0, now.Add(time.Minute)))
	values, err := rdb.HMGet(context.Background(), channelMonitorCooldownKey(13), "generation", "streak").Result()
	require.NoError(t, err)
	require.Equal(t, event.Generation, mustRedisTestInt64(t, values[0]))
	require.Equal(t, int64(event.Streak), mustRedisTestInt64(t, values[1]))
}

func TestChannelMonitorCooldownStoreKeepsFifthLadderAcrossStoreRestart(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewChannelMonitorCooldownStore(rdb)
	now := time.Unix(1000, 0).UTC()
	ladder := []int{2, 5, 30, 60, 120}
	var event service.ChannelMonitorCooldownEvent
	for i := 0; i < 6; i++ {
		var err error
		event, err = store.ObserveFailure(context.Background(), 14, now, ladder)
		require.NoError(t, err)
		now = event.Until.Add(time.Second)
	}
	require.Equal(t, 6, event.Streak)
	failureNow := now
	store = NewChannelMonitorCooldownStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	event, err := store.ObserveFailure(context.Background(), 14, failureNow, ladder)
	require.NoError(t, err)
	require.Equal(t, 7, event.Streak)
	require.Equal(t, 120*time.Minute, event.Until.Sub(failureNow))
}

func mustRedisTestInt64(t *testing.T, value any) int64 {
	t.Helper()
	parsed, err := redisInt64Value(value)
	require.NoError(t, err)
	return parsed
}
