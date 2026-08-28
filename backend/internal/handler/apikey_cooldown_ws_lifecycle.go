package handler

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type apiKeyCooldownTurnObserver interface {
	BeginAPIKeyCooldownAttempt(context.Context, *service.Account, string, bool, time.Time) (*service.APIKeyCooldownAttempt, bool, error)
	ObserveAPIKeyAttemptSuccess(context.Context, *service.Account, *service.APIKeyCooldownAttempt, time.Time) error
	ObserveAPIKeyAttemptError(context.Context, *service.Account, *service.APIKeyCooldownAttempt, error, time.Time) (service.APIKeyCooldownDecision, error)
}

// openAIWSCooldownTurnAttempts keeps one attempt and one terminal decision per
// response.create turn. A long-lived websocket must not reuse turn 1's token.
type openAIWSCooldownTurnAttempts struct {
	ctx     context.Context
	starter apiKeyCooldownTurnObserver
	account *service.Account
	mu      sync.Mutex
	items   map[int]*service.APIKeyCooldownAttempt
	closed  map[int]struct{}
}

func newOpenAIWSCooldownTurnAttempts(ctx context.Context, starter apiKeyCooldownTurnObserver, account *service.Account) *openAIWSCooldownTurnAttempts {
	if ctx == nil {
		ctx = context.Background()
	}
	return &openAIWSCooldownTurnAttempts{
		ctx: ctx, starter: starter, account: account,
		items: make(map[int]*service.APIKeyCooldownAttempt), closed: make(map[int]struct{}),
	}
}

func (t *openAIWSCooldownTurnAttempts) begin(turn int, model string) (bool, error) {
	if t == nil || t.starter == nil || t.account == nil || turn <= 0 {
		return false, nil
	}
	t.mu.Lock()
	existing := t.items[turn]
	t.mu.Unlock()
	if existing != nil {
		return false, nil
	}
	attempt, blocked, err := t.starter.BeginAPIKeyCooldownAttempt(t.ctx, t.account, model, true, time.Now().UTC())
	if err != nil || blocked || attempt == nil {
		return blocked, err
	}
	t.mu.Lock()
	t.items[turn] = attempt
	delete(t.closed, turn)
	t.mu.Unlock()
	return false, nil
}

func (t *openAIWSCooldownTurnAttempts) current(turn int) *service.APIKeyCooldownAttempt {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.items[turn]
}

func (t *openAIWSCooldownTurnAttempts) markRequestSent(turn int) {
	if attempt := t.current(turn); attempt != nil {
		attempt.MarkRequestSent()
	}
}

func (t *openAIWSCooldownTurnAttempts) markResponseStarted(turn int) {
	if attempt := t.current(turn); attempt != nil {
		attempt.MarkResponseStarted()
	}
}

func (t *openAIWSCooldownTurnAttempts) finish(turn int, turnErr error) error {
	if t == nil || t.starter == nil || t.account == nil {
		return nil
	}
	t.mu.Lock()
	if _, done := t.closed[turn]; done {
		t.mu.Unlock()
		return nil
	}
	attempt := t.items[turn]
	t.closed[turn] = struct{}{}
	delete(t.items, turn)
	t.mu.Unlock()
	if attempt == nil {
		return nil
	}
	if turnErr == nil {
		return t.starter.ObserveAPIKeyAttemptSuccess(t.ctx, t.account, attempt, time.Now().UTC())
	}
	_, err := t.starter.ObserveAPIKeyAttemptError(t.ctx, t.account, attempt, turnErr, time.Now().UTC())
	return err
}
