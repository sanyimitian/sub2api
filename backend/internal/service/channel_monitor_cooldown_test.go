package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorCooldownMemoryStoreLadderAndMerge(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	now := time.Unix(1000, 0).UTC()
	cfg := DefaultChannelMonitorCooldownSettings()
	e1, err := store.ObserveFailure(context.Background(), 7, now, cfg.CooldownMinutes)
	require.NoError(t, err)
	require.Equal(t, 1, e1.Streak)
	require.Equal(t, 2*time.Minute, e1.Until.Sub(now))
	e2, err := store.ObserveFailure(context.Background(), 7, now.Add(time.Second), cfg.CooldownMinutes)
	require.NoError(t, err)
	require.Equal(t, e1.Generation, e2.Generation)
	require.Equal(t, e1.Until, e2.Until)
	e3, err := store.ObserveFailure(context.Background(), 7, e1.Until.Add(time.Second), cfg.CooldownMinutes)
	require.NoError(t, err)
	require.Equal(t, 2, e3.Streak)
	require.Equal(t, 5*time.Minute, e3.Until.Sub(e1.Until.Add(time.Second)))
	active, err := store.IsCooling(context.Background(), 7, e3.Until.Add(-time.Second))
	require.NoError(t, err)
	require.True(t, active)
}

func TestChannelMonitorCooldownSuccessDoesNotDeleteNewGeneration(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	now := time.Unix(1000, 0).UTC()
	cfg := DefaultChannelMonitorCooldownSettings()
	e1, _ := store.ObserveFailure(context.Background(), 9, now, cfg.CooldownMinutes)
	_, _ = store.ObserveFailure(context.Background(), 9, e1.Until.Add(time.Second), cfg.CooldownMinutes)
	require.NoError(t, store.ObserveSuccess(context.Background(), 9, e1.Generation, e1.Until.Add(2*time.Second)))
	active, err := store.IsCooling(context.Background(), 9, e1.Until.Add(2*time.Second))
	require.NoError(t, err)
	require.True(t, active)
}

func TestChannelMonitorCooldownSuccessClearsStreakWithoutShorteningActiveWindow(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	now := time.Unix(1000, 0).UTC()
	event, err := store.ObserveFailure(context.Background(), 10, now, []int{2, 5, 30, 60, 120})
	require.NoError(t, err)
	require.NoError(t, store.ObserveSuccess(context.Background(), 10, event.Generation, now.Add(time.Minute)))
	current, err := store.Current(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, event.Until, current.Until)
	require.Equal(t, 0, current.Streak)
	require.True(t, current.Until.After(now.Add(time.Minute)))
}

func TestChannelMonitorCooldownZeroGenerationSuccessCannotClearFailure(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	now := time.Unix(1000, 0).UTC()
	event, err := store.ObserveFailure(context.Background(), 11, now, []int{2, 5, 30, 60, 120})
	require.NoError(t, err)
	require.NoError(t, store.ObserveSuccess(context.Background(), 11, 0, now.Add(time.Minute)))
	current, err := store.Current(context.Background(), 11)
	require.NoError(t, err)
	require.Equal(t, event.Generation, current.Generation)
	require.Equal(t, event.Streak, current.Streak)
}

func TestChannelMonitorProbeObserverIgnoresOrdinaryAndCanceledRequests(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	observer := NewChannelMonitorProbeObserver(store, nil, func(context.Context) (*ChannelMonitorCooldownSettings, error) {
		return DefaultChannelMonitorCooldownSettings(), nil
	})
	attempt := observer.Begin(context.Background(), &Account{ID: 12, Type: AccountTypeAPIKey}, time.Now())
	require.Nil(t, attempt)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Ordinary requests never produce a probe attempt, so cancellation is a
	// no-op and cannot create a cooldown event.
	observer.Finish(ctx, nil, ChannelMonitorProbeOutcome{Err: context.Canceled})
	active, err := store.IsCooling(context.Background(), 12, time.Now())
	require.NoError(t, err)
	require.False(t, active)
}

func TestChannelMonitorProbeObserverRecordsProbeClientCancellation(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	observer := NewChannelMonitorProbeObserver(store, nil, func(context.Context) (*ChannelMonitorCooldownSettings, error) {
		return DefaultChannelMonitorCooldownSettings(), nil
	})
	ctx := WithChannelMonitorProbe(context.Background(), ChannelMonitorProbe{MonitorID: 1, RequestID: "probe-cancel"})
	attempt := observer.Begin(ctx, &Account{ID: 13, Type: AccountTypeAPIKey}, time.Now())
	require.NotNil(t, attempt)
	observer.Finish(ctx, attempt, ChannelMonitorProbeOutcome{Err: context.Canceled})
	active, err := store.IsCooling(context.Background(), 13, time.Now())
	require.NoError(t, err)
	require.True(t, active)
}

func TestChannelMonitorProbeObserverRecordsOnlyOneTerminalOutcome(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	observer := NewChannelMonitorProbeObserver(store, nil, func(context.Context) (*ChannelMonitorCooldownSettings, error) {
		return DefaultChannelMonitorCooldownSettings(), nil
	})
	ctx := WithChannelMonitorProbe(context.Background(), ChannelMonitorProbe{MonitorID: 1, RequestID: "r"})
	attempt := observer.Begin(ctx, &Account{ID: 12, Type: AccountTypeAPIKey}, time.Now())
	observer.Finish(ctx, attempt, ChannelMonitorProbeOutcome{Err: errors.New("boom")})
	observer.Finish(ctx, attempt, ChannelMonitorProbeOutcome{})
	event, err := store.Current(context.Background(), 12)
	require.NoError(t, err)
	require.Equal(t, 1, event.Streak)
}

func TestChannelMonitorProbeObserverBypassesNonTargetAccountTypes(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	observer := NewChannelMonitorProbeObserver(store, nil, func(context.Context) (*ChannelMonitorCooldownSettings, error) {
		return DefaultChannelMonitorCooldownSettings(), nil
	})
	ctx := ctxWithProbe()
	accounts := []*Account{
		{ID: 1, Type: AccountTypeOAuth},
		{ID: 2, Type: AccountTypeSetupToken},
		{ID: 3, Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}},
	}
	for _, account := range accounts {
		require.Nil(t, observer.Begin(ctx, account, time.Now()))
	}
	require.NotNil(t, observer.Begin(ctx, &Account{ID: 4, Type: AccountTypeAPIKey}, time.Now()))
}

func TestChannelMonitorProbeObserverLateSuccessCannotClearNewFailure(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	observer := NewChannelMonitorProbeObserver(store, nil, func(context.Context) (*ChannelMonitorCooldownSettings, error) {
		return DefaultChannelMonitorCooldownSettings(), nil
	})
	ctx := ctxWithProbe()
	account := &Account{ID: 16, Type: AccountTypeAPIKey}
	oldAttempt := observer.Begin(ctx, account, time.Now())
	observer.Finish(ctx, observer.Begin(ctx, account, time.Now()), ChannelMonitorProbeOutcome{Err: errors.New("new failure")})
	before, err := store.Current(context.Background(), account.ID)
	require.NoError(t, err)
	observer.Finish(ctx, oldAttempt, ChannelMonitorProbeOutcome{})
	after, err := store.Current(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, before.Generation, after.Generation)
	require.Equal(t, before.Streak, after.Streak)
}

func TestChannelMonitorPriorityState(t *testing.T) {
	repo := &priorityTestRepo{account: &Account{ID: 4, Priority: 10, Extra: map[string]any{}}}
	priority := NewChannelMonitorPriorityAdjuster(repo, nil)
	cfg := DefaultChannelMonitorCooldownSettings()
	now := time.Unix(1000, 0).UTC()
	for i := 0; i < 4; i++ {
		require.NoError(t, priority.Observe(ctxWithProbe(), 4, cfg.SlowResponseThresholdSeconds+1, now.Add(time.Duration(i)*time.Second), cfg))
	}
	require.Equal(t, 3, priority.Boost(repo.account))
	require.NoError(t, priority.Observe(ctxWithProbe(), 4, 1, now.Add(time.Minute), cfg))
	require.Equal(t, 0, priority.Boost(repo.account))
}

func TestAccountEffectivePriorityIncludesTemporaryBoost(t *testing.T) {
	account := &Account{Priority: 4, Extra: map[string]any{ChannelMonitorPriorityExtraKey: ChannelMonitorPriorityState{Boost: 2, Increases: 2, FirstIncreasedAt: time.Now(), RecoverySeconds: 3600}}}
	require.Equal(t, 6, account.EffectivePriority())
}

func TestChannelMonitorPriorityThresholdRecoveryAndBaselineChange(t *testing.T) {
	repo := &priorityTestRepo{account: &Account{ID: 8, Priority: 10, Extra: map[string]any{}}}
	adjuster := NewChannelMonitorPriorityAdjuster(repo, nil)
	cfg := DefaultChannelMonitorCooldownSettings()
	now := time.Now().UTC()
	require.NoError(t, adjuster.Observe(ctxWithProbe(), 8, cfg.SlowResponseThresholdSeconds, now, cfg))
	require.Equal(t, 0, adjuster.Boost(repo.account))
	for i := 0; i < cfg.MaxPriorityIncrease; i++ {
		require.NoError(t, adjuster.Observe(ctxWithProbe(), 8, cfg.SlowResponseThresholdSeconds+1, now.Add(time.Duration(i+1)*time.Second), cfg))
	}
	require.Equal(t, cfg.MaxPriorityIncrease*cfg.PriorityIncrement, adjuster.Boost(repo.account))
	repo.account.Priority = 20
	require.Equal(t, 20+cfg.MaxPriorityIncrease*cfg.PriorityIncrement, repo.account.EffectivePriority())
	require.NoError(t, adjuster.Observe(ctxWithProbe(), 8, cfg.SlowResponseThresholdSeconds+1, now.Add(time.Duration(cfg.PriorityAutoRecoverySeconds+1)*time.Second), cfg))
	require.Equal(t, cfg.PriorityIncrement, adjuster.Boost(repo.account))
}

func TestChannelMonitorCooldownGuardOnlyAppliesToSignedProbeContext(t *testing.T) {
	store := NewMemoryChannelMonitorCooldownStore()
	now := time.Now().UTC()
	_, err := store.ObserveFailure(context.Background(), 44, now, []int{2, 5, 30, 60, 120})
	require.NoError(t, err)
	svc := &GatewayService{channelMonitorCooldown: store}
	require.False(t, svc.channelMonitorAccountCooling(context.Background(), 44))
	ctx := WithChannelMonitorProbe(context.Background(), ChannelMonitorProbe{MonitorID: 1, RequestID: "probe"})
	require.True(t, svc.channelMonitorAccountCooling(ctx, 44))
}

func ctxWithProbe() context.Context {
	return WithChannelMonitorProbe(context.Background(), ChannelMonitorProbe{MonitorID: 1, RequestID: "r"})
}

type priorityTestRepo struct {
	mu      sync.Mutex
	account *Account
}

func (r *priorityTestRepo) GetByID(context.Context, int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.account, nil
}
func (r *priorityTestRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.Extra == nil {
		r.account.Extra = map[string]any{}
	}
	for k, v := range updates {
		r.account.Extra[k] = v
	}
	return nil
}
