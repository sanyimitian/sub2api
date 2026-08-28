package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	APIKeyFirstValidContentTimeout                            = 29 * time.Second
	apiKeyFirstValidContentTimeoutReason GatewayFailureReason = "first_valid_content_timeout"
)

var ErrAPIKeyFirstValidContentTimeout = &UpstreamFailoverError{
	StatusCode:   http.StatusGatewayTimeout,
	ResponseBody: []byte(`{"error":{"type":"first_valid_content_timeout","message":"Upstream produced no valid content before the deadline"}}`),
	Scope:        GatewayFailureScopeAccount,
	Reason:       apiKeyFirstValidContentTimeoutReason,
}

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

	clientContext      context.Context
	requestSent        atomic.Bool
	responseStarted    atomic.Bool
	validContent       atomic.Bool
	firstValidTimedOut atomic.Bool
	firstValidMu       sync.Mutex
	firstValidTimer    *time.Timer
	firstValidCancelID uint64
	firstValidCancels  map[uint64]context.CancelCauseFunc
	terminalOnce       sync.Once
	terminalErr        error
	terminalDecision   APIKeyCooldownDecision
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

func (a *APIKeyCooldownAttempt) MarkValidContent() {
	if a == nil || a.firstValidTimedOut.Load() || !a.validContent.CompareAndSwap(false, true) {
		return
	}
	a.stopFirstValidContentGuard()
}

// MarkFirstValidContentTimedOut records an externally enforced first-content
// deadline, such as the WebSocket semantic-frame guard.
func (a *APIKeyCooldownAttempt) MarkFirstValidContentTimedOut() {
	if a == nil || a.ValidContentStarted() {
		return
	}
	a.firstValidTimedOut.Store(true)
}

func (a *APIKeyCooldownAttempt) ValidContentStarted() bool {
	return a != nil && a.validContent.Load()
}

func (a *APIKeyCooldownAttempt) FirstValidContentTimedOut() bool {
	return a != nil && a.firstValidTimedOut.Load()
}

func IsAPIKeyFirstValidContentTimeout(err error) bool {
	var upstreamErr *UpstreamFailoverError
	return errors.As(err, &upstreamErr) && upstreamErr.Reason == apiKeyFirstValidContentTimeoutReason
}

func (a *APIKeyCooldownAttempt) bindFirstValidContentContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancelCause(parent)
	if a == nil || timeout <= 0 || a.ValidContentStarted() {
		return ctx, func() { cancel(context.Canceled) }
	}

	a.firstValidMu.Lock()
	if a.ValidContentStarted() {
		a.firstValidMu.Unlock()
		return ctx, func() { cancel(context.Canceled) }
	}
	if a.FirstValidContentTimedOut() {
		a.firstValidMu.Unlock()
		cancel(ErrAPIKeyFirstValidContentTimeout)
		return ctx, func() {}
	}
	if a.firstValidCancels == nil {
		a.firstValidCancels = make(map[uint64]context.CancelCauseFunc)
	}
	a.firstValidCancelID++
	cancelID := a.firstValidCancelID
	a.firstValidCancels[cancelID] = cancel
	if a.firstValidTimer == nil {
		a.firstValidTimer = time.AfterFunc(timeout, a.fireFirstValidContentTimeout)
	}
	a.firstValidMu.Unlock()

	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			a.firstValidMu.Lock()
			delete(a.firstValidCancels, cancelID)
			a.firstValidMu.Unlock()
			cancel(context.Canceled)
		})
	}
}

func (a *APIKeyCooldownAttempt) fireFirstValidContentTimeout() {
	if a == nil {
		return
	}
	a.firstValidMu.Lock()
	if a.ValidContentStarted() || a.FirstValidContentTimedOut() {
		a.firstValidMu.Unlock()
		return
	}
	a.MarkFirstValidContentTimedOut()
	cancels := make([]context.CancelCauseFunc, 0, len(a.firstValidCancels))
	for _, cancel := range a.firstValidCancels {
		cancels = append(cancels, cancel)
	}
	a.firstValidCancels = nil
	a.firstValidMu.Unlock()
	for _, cancel := range cancels {
		cancel(ErrAPIKeyFirstValidContentTimeout)
	}
}

func (a *APIKeyCooldownAttempt) stopFirstValidContentGuard() {
	if a == nil {
		return
	}
	a.firstValidMu.Lock()
	if a.firstValidTimer != nil {
		a.firstValidTimer.Stop()
		a.firstValidTimer = nil
	}
	a.firstValidCancels = nil
	a.firstValidMu.Unlock()
}

func (a *APIKeyCooldownAttempt) completeSuccess(observe func() error) error {
	if a == nil {
		return nil
	}
	a.MarkValidContent()
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
	a.stopFirstValidContentGuard()
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
	ctx = context.WithValue(ctx, apiKeyCooldownAttemptContextKey{}, attempt)
	ctx, _ = attempt.bindFirstValidContentContext(ctx, APIKeyFirstValidContentTimeout)
	return ctx
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
