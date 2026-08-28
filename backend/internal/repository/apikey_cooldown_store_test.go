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

func TestRedisAPIKeyCooldownStoreMergesConcurrentFailuresAndPreservesGenerations(t *testing.T) {
	mini, err := miniredis.Run()
	require.NoError(t, err)
	defer mini.Close()

	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	store := NewAPIKeyCooldownStore(client)
	key := service.APIKeyCooldownKey{AccountID: 42, Family: service.APIKeyFailureUnknown, Scope: service.APIKeyCooldownScopeAccount}
	policy := service.APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{60, 600, 1800}, Mode: service.APIKeyCooldownModeCycle}

	const callers = 100
	results := make(chan service.APIKeyCooldownEvent, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event, callErr := store.ObserveFailure(context.Background(), key, policy, time.Now(), nil)
			require.NoError(t, callErr)
			results <- event
		}()
	}
	wg.Wait()
	close(results)

	var first service.APIKeyCooldownEvent
	created := 0
	for event := range results {
		if first.Generation == 0 {
			first = event
		}
		if event.Created {
			created++
		}
		require.Equal(t, first.Generation, event.Generation)
		require.Equal(t, int64(1), event.Streak)
	}
	require.Equal(t, 1, created)

	second, err := store.ObserveFailure(context.Background(), key, policy, first.Until, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), second.Streak)
	require.NotEqual(t, first.Generation, second.Generation)

	stale := service.APIKeyCooldownAttemptToken{Generations: map[string]int64{key.RedisKey(): first.Generation}}
	require.NoError(t, store.ResetSuccess(context.Background(), []service.APIKeyCooldownKey{key}, stale, second.Until.Add(time.Second)))
	current, active, err := store.Check(context.Background(), key, second.Until.Add(-time.Second))
	require.NoError(t, err)
	require.True(t, active)
	require.Equal(t, second.Generation, current.Generation)
}

func TestRedisAPIKeyCooldownStoreSeparatesFailureFamiliesAndModels(t *testing.T) {
	mini, err := miniredis.Run()
	require.NoError(t, err)
	defer mini.Close()
	store := NewAPIKeyCooldownStore(redis.NewClient(&redis.Options{Addr: mini.Addr()}))
	now := time.Now()
	policy := service.APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{10, 20}, Mode: service.APIKeyCooldownModeHoldLast}

	rateKey := service.APIKeyCooldownKey{AccountID: 9, Family: service.APIKeyFailureRateLimit, Scope: service.APIKeyCooldownScopeAccount}
	transientKey := service.APIKeyCooldownKey{AccountID: 9, Family: service.APIKeyFailureTransientUpstream, Scope: service.APIKeyCooldownScopeAccount}
	modelA := service.APIKeyCooldownKey{AccountID: 9, Model: "GPT-5", Family: service.APIKeyFailureOverload, Scope: service.APIKeyCooldownScopeModel}
	modelB := service.APIKeyCooldownKey{AccountID: 9, Model: "gpt-4", Family: service.APIKeyFailureOverload, Scope: service.APIKeyCooldownScopeModel}

	rate, err := store.ObserveFailure(context.Background(), rateKey, policy, now, nil)
	require.NoError(t, err)
	transient, err := store.ObserveFailure(context.Background(), transientKey, policy, now, nil)
	require.NoError(t, err)
	a, err := store.ObserveFailure(context.Background(), modelA, policy, now, nil)
	require.NoError(t, err)
	b, err := store.ObserveFailure(context.Background(), modelB, policy, now, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), rate.Streak)
	require.Equal(t, int64(1), transient.Streak)
	require.Equal(t, int64(1), a.Streak)
	require.Equal(t, int64(1), b.Streak)
	require.NotEqual(t, a.Key.RedisKey(), b.Key.RedisKey())
}

func TestRedisAPIKeyCooldownStoreUsesDynamicRetentionTTL(t *testing.T) {
	mini := miniredis.RunT(t)
	store := NewAPIKeyCooldownStore(redis.NewClient(&redis.Options{Addr: mini.Addr()}))
	now := time.Now().UTC().Truncate(time.Millisecond)

	longPolicy := service.APIKeyCooldownPolicy{
		Enabled:   true,
		Cooldowns: []int{8 * 24 * 60 * 60},
		Mode:      service.APIKeyCooldownModeHoldLast,
	}
	key := service.APIKeyCooldownKey{AccountID: 100, Family: service.APIKeyFailureTransientUpstream, Scope: service.APIKeyCooldownScopeAccount}
	_, err := store.ObserveFailure(context.Background(), key, longPolicy, now, nil)
	require.NoError(t, err)

	redisStore, ok := store.(*apiKeyCooldownStore)
	require.True(t, ok)
	ttl, err := redisStore.rdb.TTL(context.Background(), key.RedisKey()).Result()
	require.NoError(t, err)
	// The retention window is added after the longest configured active window.
	require.GreaterOrEqual(t, ttl, 15*24*60*60*time.Second)

	reset := now.Add(10 * 24 * time.Hour)
	rateKey := service.APIKeyCooldownKey{AccountID: 101, Family: service.APIKeyFailureRateLimit, Scope: service.APIKeyCooldownScopeAccount}
	_, err = store.ObserveFailure(context.Background(), rateKey, service.APIKeyCooldownPolicy{
		Enabled: true, Cooldowns: []int{60}, Mode: service.APIKeyCooldownModeHoldLast,
	}, now, &reset)
	require.NoError(t, err)
	ttl, err = redisStore.rdb.TTL(context.Background(), rateKey.RedisKey()).Result()
	require.NoError(t, err)
	require.GreaterOrEqual(t, ttl, 17*24*60*60*time.Second)
}

func TestRedisAPIKeyCooldownStoreActiveMergeRestoresShortenedTTL(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	store := NewAPIKeyCooldownStore(client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	key := service.APIKeyCooldownKey{AccountID: 102, Family: service.APIKeyFailureUnknown, Scope: service.APIKeyCooldownScopeAccount}
	policy := service.APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{60}, Mode: service.APIKeyCooldownModeHoldLast}
	first, err := store.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)
	require.NoError(t, client.Expire(context.Background(), key.RedisKey(), time.Second).Err())
	require.NoError(t, client.Expire(context.Background(), key.RedisKey()+":generation", time.Second).Err())

	merged, err := store.ObserveFailure(context.Background(), key, policy, now.Add(time.Second), nil)
	require.NoError(t, err)
	require.False(t, merged.Created)
	require.Equal(t, first.Generation, merged.Generation)
	ttl, err := client.TTL(context.Background(), key.RedisKey()).Result()
	require.NoError(t, err)
	require.GreaterOrEqual(t, ttl, 7*24*60*60*time.Second)
}

func TestRedisAPIKeyCooldownStoreActiveMergeKeepsLongerUpstreamReset(t *testing.T) {
	mini := miniredis.RunT(t)
	store := NewAPIKeyCooldownStore(redis.NewClient(&redis.Options{Addr: mini.Addr()}))
	now := time.Now().UTC().Truncate(time.Millisecond)
	key := service.APIKeyCooldownKey{AccountID: 104, Family: service.APIKeyFailureRateLimit, Scope: service.APIKeyCooldownScopeAccount}
	policy := service.APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{60}, Mode: service.APIKeyCooldownModeHoldLast}
	first, err := store.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)
	require.NoError(t, store.MarkPersisted(context.Background(), key, first.Generation))
	upstreamReset := now.Add(5 * time.Minute)
	merged, err := store.ObserveFailure(context.Background(), key, policy, now.Add(time.Second), &upstreamReset)
	require.NoError(t, err)
	require.False(t, merged.Created)
	require.Equal(t, first.Generation, merged.Generation)
	require.Equal(t, first.Streak, merged.Streak)
	require.Equal(t, upstreamReset, merged.Until)
	require.True(t, merged.NeedsPersistence, "an extended active event must persist the later deadline")
}

func TestRedisAPIKeyCooldownStoreGenerationSafeMultiKeySuccessReset(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	store := NewAPIKeyCooldownStore(client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	policy := service.APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{1}, Mode: service.APIKeyCooldownModeHoldLast}
	accountKey := service.APIKeyCooldownKey{AccountID: 103, Family: service.APIKeyFailureTransientUpstream, Scope: service.APIKeyCooldownScopeAccount}
	modelKey := service.APIKeyCooldownKey{AccountID: 103, Model: "model-a", Family: service.APIKeyFailureOverload, Scope: service.APIKeyCooldownScopeModel}
	accountEvent, err := store.ObserveFailure(context.Background(), accountKey, policy, now, nil)
	require.NoError(t, err)
	modelEvent, err := store.ObserveFailure(context.Background(), modelKey, policy, now, nil)
	require.NoError(t, err)
	token := service.APIKeyCooldownAttemptToken{Generations: map[string]int64{
		accountKey.RedisKey(): accountEvent.Generation,
		modelKey.RedisKey():   modelEvent.Generation,
	}}

	// Success while either event is active must not clear either key.
	require.NoError(t, store.ResetSuccess(context.Background(), []service.APIKeyCooldownKey{accountKey, modelKey}, token, now.Add(500*time.Millisecond)))
	_, active, err := store.Check(context.Background(), accountKey, now.Add(500*time.Millisecond))
	require.NoError(t, err)
	require.True(t, active)
	_, active, err = store.Check(context.Background(), modelKey, now.Add(500*time.Millisecond))
	require.NoError(t, err)
	require.True(t, active)

	// Once both windows have expired, one atomic call clears both states.
	expired := now.Add(2 * time.Second)
	require.NoError(t, store.ResetSuccess(context.Background(), []service.APIKeyCooldownKey{accountKey, modelKey}, token, expired))
	_, active, err = store.Check(context.Background(), accountKey, expired)
	require.NoError(t, err)
	require.False(t, active)
	_, active, err = store.Check(context.Background(), modelKey, expired)
	require.NoError(t, err)
	require.False(t, active)

	// A stale token cannot clear the next generation.
	next, err := store.ObserveFailure(context.Background(), accountKey, policy, expired, nil)
	require.NoError(t, err)
	require.Greater(t, next.Generation, accountEvent.Generation)
	stale := service.APIKeyCooldownAttemptToken{Generations: map[string]int64{accountKey.RedisKey(): accountEvent.Generation}}
	require.NoError(t, store.ResetSuccess(context.Background(), []service.APIKeyCooldownKey{accountKey}, stale, expired.Add(2*time.Second)))
	_, active, err = store.Check(context.Background(), accountKey, expired.Add(500*time.Millisecond))
	require.NoError(t, err)
	require.True(t, active)
}

func TestRedisAPIKeyCooldownStoreCrossInstanceCycle(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	firstStore := NewAPIKeyCooldownStore(client)
	secondStore := NewAPIKeyCooldownStore(client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	key := service.APIKeyCooldownKey{AccountID: 105, Family: service.APIKeyFailureUnknown, Scope: service.APIKeyCooldownScopeAccount}
	policy := service.APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{1, 2, 3}, Mode: service.APIKeyCooldownModeCycle}

	first, err := firstStore.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)

	current := first
	for index, attemptID := range []string{"attempt-2", "attempt-3", "attempt-4"} {
		startedAt := current.Until
		current, err = secondStore.ObserveFailure(context.Background(), key, policy, startedAt, nil)
		require.NoError(t, err)
		require.Equal(t, int64(index+2), current.Streak)
		if attemptID == "attempt-4" {
			require.Equal(t, time.Second, current.Until.Sub(startedAt))
		}
	}
}
