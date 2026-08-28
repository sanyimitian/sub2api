package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// APIKeyCooldownAttempt carries the shared guard generation and lifecycle of
// one concrete upstream dispatch. It is safe to share with response writers
// and transport callbacks for the duration of the attempt.
type APIKeyCooldownAttempt struct {
	ID         string
	AccountID  int64
	Model      string
	StartedAt  time.Time
	Token      APIKeyCooldownAttemptToken
	ReplaySafe bool

	clientContext    context.Context
	requestSent      atomic.Bool
	responseStarted  atomic.Bool
	terminalOnce     sync.Once
	terminalErr      error
	terminalDecision APIKeyCooldownDecision
}

func (a *APIKeyCooldownAttempt) ClientCanceled() bool {
	return a != nil && a.clientContext != nil && a.clientContext.Err() == context.Canceled
}

func (a *APIKeyCooldownAttempt) ClientTimedOut() bool {
	return a != nil && a.clientContext != nil && a.clientContext.Err() == context.DeadlineExceeded
}

func (a *APIKeyCooldownAttempt) MarkRequestSent() {
	if a != nil {
		a.requestSent.Store(true)
	}
}

func (a *APIKeyCooldownAttempt) RequestSent() bool {
	return a != nil && a.requestSent.Load()
}

func (a *APIKeyCooldownAttempt) MarkResponseStarted() {
	if a != nil {
		a.responseStarted.Store(true)
	}
}

func (a *APIKeyCooldownAttempt) ResponseStarted() bool {
	return a != nil && a.responseStarted.Load()
}

func (a *APIKeyCooldownAttempt) completeSuccess(observe func() error) error {
	if a == nil {
		return nil
	}
	a.terminalOnce.Do(func() {
		if observe != nil {
			a.terminalErr = observe()
		}
	})
	return a.terminalErr
}

func (a *APIKeyCooldownAttempt) completeFailure(observe func() (APIKeyCooldownDecision, error)) (APIKeyCooldownDecision, error) {
	if a == nil {
		if observe == nil {
			return APIKeyCooldownDecision{}, nil
		}
		return observe()
	}
	a.terminalOnce.Do(func() {
		if observe != nil {
			a.terminalDecision, a.terminalErr = observe()
		}
	})
	return a.terminalDecision, a.terminalErr
}

type apiKeyCooldownAttemptContextKey struct{}

func ContextWithAPIKeyCooldownAttempt(ctx context.Context, attempt *APIKeyCooldownAttempt) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if attempt == nil {
		return ctx
	}
	return context.WithValue(ctx, apiKeyCooldownAttemptContextKey{}, attempt)
}

func APIKeyCooldownAttemptFromContext(ctx context.Context) *APIKeyCooldownAttempt {
	if ctx == nil {
		return nil
	}
	attempt, _ := ctx.Value(apiKeyCooldownAttemptContextKey{}).(*APIKeyCooldownAttempt)
	return attempt
}

func (a *APIKeyCooldownAttempt) successObservation() APIKeyCooldownSuccess {
	if a == nil {
		return APIKeyCooldownSuccess{}
	}
	return APIKeyCooldownSuccess{
		AttemptID:      a.ID,
		AccountID:      a.AccountID,
		Model:          a.Model,
		AttemptStarted: a.StartedAt,
		AttemptToken:   a.Token,
	}
}
