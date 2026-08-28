package handler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type wsCooldownLifecycleObserverFake struct {
	mu       sync.Mutex
	started  []*service.APIKeyCooldownAttempt
	success  []string
	failures []string
}

func (f *wsCooldownLifecycleObserverFake) BeginAPIKeyCooldownAttempt(_ context.Context, account *service.Account, model string, replaySafe bool, now time.Time) (*service.APIKeyCooldownAttempt, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	attempt := &service.APIKeyCooldownAttempt{ID: model, AccountID: account.ID, Model: model, StartedAt: now, ReplaySafe: replaySafe}
	f.started = append(f.started, attempt)
	return attempt, false, nil
}

func (f *wsCooldownLifecycleObserverFake) ObserveAPIKeyAttemptSuccess(_ context.Context, _ *service.Account, attempt *service.APIKeyCooldownAttempt, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.success = append(f.success, attempt.ID)
	return nil
}

func (f *wsCooldownLifecycleObserverFake) ObserveAPIKeyAttemptError(_ context.Context, _ *service.Account, attempt *service.APIKeyCooldownAttempt, _ error, _ time.Time) (service.APIKeyCooldownDecision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, attempt.ID)
	return service.APIKeyCooldownDecision{}, nil
}

func TestOpenAIWSCooldownTurnAttemptsAreIndependentAndTerminalOnce(t *testing.T) {
	fake := &wsCooldownLifecycleObserverFake{}
	turns := newOpenAIWSCooldownTurnAttempts(context.Background(), fake, &service.Account{ID: 42, Type: service.AccountTypeAPIKey})

	blocked, err := turns.begin(1, "gpt-first")
	require.NoError(t, err)
	require.False(t, blocked)
	blocked, err = turns.begin(2, "gpt-second")
	require.NoError(t, err)
	require.False(t, blocked)
	blocked, err = turns.begin(2, "gpt-second")
	require.NoError(t, err)
	require.False(t, blocked)
	require.Len(t, fake.started, 2, "one logical websocket turn must keep one cooldown attempt across internal transport retries")

	turns.markRequestSent(1)
	turns.markResponseStarted(2)
	require.True(t, fake.started[0].RequestSent())
	require.False(t, fake.started[0].ResponseStarted())
	require.False(t, fake.started[1].RequestSent())
	require.True(t, fake.started[1].ResponseStarted())

	require.NoError(t, turns.finish(1, nil))
	require.NoError(t, turns.finish(1, errors.New("late duplicate")))
	require.NoError(t, turns.finish(2, errors.New("upstream reset")))
	require.Equal(t, []string{"gpt-first"}, fake.success)
	require.Equal(t, []string{"gpt-second"}, fake.failures)
}
