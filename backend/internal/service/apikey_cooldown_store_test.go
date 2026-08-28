package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyCooldownKeyIsVersionedAndIndependentOfGroup(t *testing.T) {
	accountKey := APIKeyCooldownKey{AccountID: 42, Family: APIKeyFailureRateLimit, Scope: APIKeyCooldownScopeAccount}
	modelKey := APIKeyCooldownKey{AccountID: 42, Model: " GPT-5 ", Family: APIKeyFailureOverload, Scope: APIKeyCooldownScopeModel}
	require.Equal(t, "apikey_cooldown:v1:{42}:account:rate_limit", accountKey.RedisKey())
	require.Equal(t, "gpt-5", modelKey.NormalizedModel())
	require.NotContains(t, modelKey.RedisKey(), "group")
	require.NotEqual(t, modelKey.RedisKey(), (APIKeyCooldownKey{AccountID: 42, Model: "gpt-4", Family: APIKeyFailureOverload, Scope: APIKeyCooldownScopeModel}).RedisKey())
}

func TestMemoryAPIKeyCooldownStoreMergesActiveFailuresAndUpgradesAfterExpiry(t *testing.T) {
	store := NewMemoryAPIKeyCooldownStore()
	key := APIKeyCooldownKey{AccountID: 42, Family: APIKeyFailureUnknown, Scope: APIKeyCooldownScopeAccount}
	policy := APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{60, 600, 1800}, Mode: APIKeyCooldownModeCycle}
	now := time.Unix(1_000, 0)

	first, err := store.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)
	require.True(t, first.Created)
	require.Equal(t, int64(1), first.Streak)
	require.Equal(t, now.UTC().Add(time.Minute), first.Until)

	merged, err := store.ObserveFailure(context.Background(), key, policy, now.Add(10*time.Second), nil)
	require.NoError(t, err)
	require.False(t, merged.Created)
	require.Equal(t, first.Generation, merged.Generation)
	require.Equal(t, first.Until, merged.Until)

	second, err := store.ObserveFailure(context.Background(), key, policy, first.Until, nil)
	require.NoError(t, err)
	require.True(t, second.Created)
	require.Equal(t, int64(2), second.Streak)
	require.Equal(t, 10*time.Minute, second.Until.Sub(first.Until))

	third, err := store.ObserveFailure(context.Background(), key, policy, second.Until, nil)
	require.NoError(t, err)
	require.Equal(t, int64(3), third.Streak)
	fourth, err := store.ObserveFailure(context.Background(), key, policy, third.Until, nil)
	require.NoError(t, err)
	require.Equal(t, int64(4), fourth.Streak)
	require.Equal(t, time.Minute, fourth.Until.Sub(third.Until))
}

func TestMemoryAPIKeyCooldownStoreSuccessUsesGenerationAndDoesNotClearActiveEvent(t *testing.T) {
	store := NewMemoryAPIKeyCooldownStore()
	key := APIKeyCooldownKey{AccountID: 42, Family: APIKeyFailureTransientUpstream, Scope: APIKeyCooldownScopeAccount}
	policy := APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{30}, Mode: APIKeyCooldownModeHoldLast}
	now := time.Unix(2_000, 0)
	event, err := store.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)
	token := APIKeyCooldownAttemptToken{Generations: map[string]int64{key.RedisKey(): event.Generation}}

	require.NoError(t, store.ResetSuccess(context.Background(), []APIKeyCooldownKey{key}, token, now.Add(time.Second)))
	active, ok, err := store.Check(context.Background(), key, now.Add(time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, event.Generation, active.Generation)

	require.NoError(t, store.ResetSuccess(context.Background(), []APIKeyCooldownKey{key}, token, event.Until))
	_, ok, err = store.Check(context.Background(), key, event.Until)
	require.NoError(t, err)
	require.False(t, ok)

	newEvent, err := store.ObserveFailure(context.Background(), key, policy, event.Until, nil)
	require.NoError(t, err)
	staleToken := APIKeyCooldownAttemptToken{Generations: map[string]int64{key.RedisKey(): event.Generation}}
	require.NoError(t, store.ResetSuccess(context.Background(), []APIKeyCooldownKey{key}, staleToken, newEvent.Until.Add(time.Second)))
	current, ok, err := store.Check(context.Background(), key, newEvent.Until.Add(-time.Second))
	require.NoError(t, err)
	require.True(t, ok, "stale success must not clear a newer event")
	require.Equal(t, newEvent.Generation, current.Generation)
}

func TestMemoryAPIKeyCooldownStoreCheckReturnsExpiredGenerationForSuccessReset(t *testing.T) {
	store := NewMemoryAPIKeyCooldownStore()
	now := time.Unix(2_500, 0).UTC()
	key := APIKeyCooldownKey{AccountID: 23, Family: APIKeyFailureTransientUpstream, Scope: APIKeyCooldownScopeAccount}
	policy := APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{1}, Mode: APIKeyCooldownModeHoldLast}

	event, err := store.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)
	expired, active, err := store.Check(context.Background(), key, event.Until.Add(time.Second))
	require.NoError(t, err)
	require.False(t, active)
	require.Equal(t, event.Generation, expired.Generation)

	token := APIKeyCooldownAttemptToken{Generations: map[string]int64{key.RedisKey(): expired.Generation}}
	require.NoError(t, store.ResetSuccess(context.Background(), []APIKeyCooldownKey{key}, token, event.Until.Add(2*time.Second)))
	cleared, _, err := store.Check(context.Background(), key, event.Until.Add(3*time.Second))
	require.NoError(t, err)
	require.Zero(t, cleared.Generation)
}

func TestMemoryAPIKeyCooldownStoreExtendedActiveEventNeedsPersistenceAgain(t *testing.T) {
	store := NewMemoryAPIKeyCooldownStore()
	now := time.Unix(2_750, 0).UTC()
	key := APIKeyCooldownKey{AccountID: 29, Family: APIKeyFailureRateLimit, Scope: APIKeyCooldownScopeAccount}
	policy := APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{60}, Mode: APIKeyCooldownModeHoldLast}

	first, err := store.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)
	require.NoError(t, store.MarkPersisted(context.Background(), key, first.Generation))

	upstreamReset := first.Until.Add(5 * time.Minute)
	extended, err := store.ObserveFailure(context.Background(), key, policy, now.Add(time.Second), &upstreamReset)
	require.NoError(t, err)
	require.False(t, extended.Created)
	require.Equal(t, first.Generation, extended.Generation)
	require.Equal(t, first.Streak, extended.Streak)
	require.Equal(t, upstreamReset, extended.Until)
	require.True(t, extended.NeedsPersistence, "an extended active event must persist the later deadline")
}

func TestMemoryAPIKeyCooldownStoreConcurrentFailuresCreateOneEvent(t *testing.T) {
	store := NewMemoryAPIKeyCooldownStore()
	key := APIKeyCooldownKey{AccountID: 7, Family: APIKeyFailureRateLimit, Scope: APIKeyCooldownScopeAccount}
	policy := APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{60, 300}, Mode: APIKeyCooldownModeHoldLast}
	now := time.Unix(3_000, 0)

	const callers = 100
	results := make(chan APIKeyCooldownEvent, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event, err := store.ObserveFailure(context.Background(), key, policy, now, nil)
			require.NoError(t, err)
			results <- event
		}()
	}
	wg.Wait()
	close(results)

	var first APIKeyCooldownEvent
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
}
