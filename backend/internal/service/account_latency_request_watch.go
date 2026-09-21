package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// AccountLatencyRequestWatch observes one upstream attempt until its first
// semantic token. It only cancels the attempt after the monitor has switched
// the failed dynamic account to a verified standby account.
type AccountLatencyRequestWatch struct {
	monitor   *AccountLatencyMonitor
	cfg       AccountLatencyMonitorGroup
	accountID int64
	startedAt time.Time

	ctx    context.Context
	cancel context.CancelCauseFunc

	switchCtx        context.Context
	cancelSwitch     context.CancelCauseFunc
	evaluationCtx    context.Context
	cancelEvaluation context.CancelFunc
	done             chan struct{}

	mu               sync.Mutex
	completed        bool
	firstTokenSeen   bool
	deadlineObserved bool
	thresholdChecked bool
	immediateChecked bool
	evaluating       bool
	evaluationDone   chan struct{}
	disarmed         bool
	switched         bool
	backupID         int64
	retryExcludedIDs map[int64]struct{}
	thresholdTimer   *time.Timer
	immediateTimer   *time.Timer
	doneOnce         sync.Once
}

func (m *AccountLatencyMonitor) StartRequestWatch(parent context.Context, groupID, accountID int64) (context.Context, *AccountLatencyRequestWatch) {
	if parent == nil {
		parent = context.Background()
	}
	if m == nil || groupID <= 0 || accountID <= 0 {
		return parent, nil
	}
	cfg, enabled := m.cachedGroup(groupID)
	if !enabled {
		return parent, nil
	}

	watchCtx, cancel := context.WithCancelCause(parent)
	switchCtx, cancelSwitch := context.WithCancelCause(context.Background())
	evaluationCtx, cancelEvaluation := context.WithCancel(context.Background())
	watch := &AccountLatencyRequestWatch{
		monitor:          m,
		cfg:              cfg,
		accountID:        accountID,
		startedAt:        time.Now(),
		ctx:              watchCtx,
		cancel:           cancel,
		switchCtx:        switchCtx,
		cancelSwitch:     cancelSwitch,
		evaluationCtx:    evaluationCtx,
		cancelEvaluation: cancelEvaluation,
		done:             make(chan struct{}),
	}
	watchCtx = withUpstreamAttemptObserver(watchCtx, watch, watch.startedAt)
	watch.ctx = watchCtx
	m.registerRequestWatch(watch)
	if _, stillEnabled := m.cachedGroup(groupID); !stillEnabled {
		watch.disarm()
		return parent, nil
	}

	threshold := time.Duration(cfg.LatencyThresholdSec) * time.Second
	immediate := time.Duration(cfg.ImmediateThresholdSec) * time.Second
	watch.mu.Lock()
	if !watch.switched {
		watch.thresholdTimer = time.AfterFunc(threshold, func() { watch.observeDeadline(false) })
		watch.immediateTimer = time.AfterFunc(immediate, func() { watch.observeDeadline(true) })
	}
	watch.mu.Unlock()
	return watchCtx, watch
}

func (w *AccountLatencyRequestWatch) observeFirstToken(elapsed time.Duration) bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	disarmed := w.disarmed
	w.mu.Unlock()
	if disarmed {
		return true
	}
	if elapsed >= time.Duration(w.cfg.ImmediateThresholdSec)*time.Second {
		w.observeDeadline(true)
	} else if elapsed >= time.Duration(w.cfg.LatencyThresholdSec)*time.Second {
		w.observeDeadline(false)
	}

	for {
		w.mu.Lock()
		if w.disarmed {
			w.mu.Unlock()
			return true
		}
		if w.completed || w.switched {
			w.mu.Unlock()
			return false
		}
		if w.evaluating {
			done := w.evaluationDone
			w.mu.Unlock()
			<-done
			continue
		}
		w.firstTokenSeen = true
		w.stopTimersLocked()
		w.mu.Unlock()
		return true
	}
}

func (w *AccountLatencyRequestWatch) observeDeadline(immediate bool) {
	for {
		w.mu.Lock()
		if w.disarmed || w.completed || w.firstTokenSeen || w.switched || (w.ctx.Err() != nil && !errors.Is(context.Cause(w.ctx), errUpstreamAttemptFirstOutputCanceled)) {
			w.mu.Unlock()
			return
		}
		if (immediate && w.immediateChecked) || (!immediate && w.thresholdChecked) {
			w.mu.Unlock()
			return
		}
		if w.evaluating {
			done := w.evaluationDone
			w.mu.Unlock()
			<-done
			continue
		}

		w.evaluating = true
		w.evaluationDone = make(chan struct{})
		if immediate {
			w.immediateChecked = true
		} else {
			w.thresholdChecked = true
		}
		addIssue := !w.deadlineObserved
		w.deadlineObserved = true
		w.mu.Unlock()

		_, _ = w.monitor.recordFirstTokenDeadlineAndSwitch(w.evaluationCtx, w.cfg, w.accountID, time.Since(w.startedAt), immediate, addIssue)

		w.mu.Lock()
		w.evaluating = false
		close(w.evaluationDone)
		w.evaluationDone = nil
		w.mu.Unlock()
		return
	}
}

// Complete stops the observer. A non-nil error means the old attempt must be
// discarded and replayed on the standby account selected by the monitor.
func (w *AccountLatencyRequestWatch) Complete(firstTokenMs *int, success bool) *UpstreamFailoverError {
	if w == nil {
		return nil
	}
	if firstTokenMs != nil {
		w.observeFirstToken(time.Duration(*firstTokenMs) * time.Millisecond)
	} else if time.Since(w.startedAt) >= time.Duration(w.cfg.LatencyThresholdSec)*time.Second {
		if time.Since(w.startedAt) >= time.Duration(w.cfg.ImmediateThresholdSec)*time.Second {
			w.observeDeadline(true)
		} else {
			w.observeDeadline(false)
		}
	}

	for {
		w.mu.Lock()
		if w.evaluating {
			done := w.evaluationDone
			w.mu.Unlock()
			<-done
			continue
		}
		if !w.completed {
			w.completed = true
			w.stopTimersLocked()
		}
		handled := w.deadlineObserved
		disarmed := w.disarmed
		switched := w.switched
		w.mu.Unlock()
		w.cancelEvaluation()
		w.monitor.unregisterRequestWatch(w)
		w.doneOnce.Do(func() { close(w.done) })

		if handled && !disarmed && !switched {
			w.monitor.completeDeadlineObservedRequest(w.cfg, w.accountID, firstTokenMs, success)
		}
		if !switched {
			return nil
		}
		return &UpstreamFailoverError{
			StatusCode:               http.StatusGatewayTimeout,
			ResponseBody:             []byte(`{"error":{"type":"first_token_timeout","message":"Upstream produced no first token before the account latency deadline"}}`),
			Stage:                    GatewayFailureStageInference,
			Scope:                    GatewayFailureScopeAccount,
			Reason:                   GatewayFailureReason("account_latency_first_token_timeout"),
			ClientStatusCode:         http.StatusGatewayTimeout,
			ClientMessage:            "Upstream produced no first token before the account latency deadline",
			SafeToFailoverAfterWrite: true,
		}
	}
}

func (w *AccountLatencyRequestWatch) switchToBackup(backupID int64, excluded map[int64]struct{}) bool {
	if w == nil || backupID <= 0 {
		return false
	}
	w.mu.Lock()
	if w.completed || w.firstTokenSeen || w.switched {
		w.mu.Unlock()
		return false
	}
	w.switched = true
	w.backupID = backupID
	w.retryExcludedIDs = cloneAccountLatencyMonitorIDSet(excluded)
	w.stopTimersLocked()
	w.mu.Unlock()
	w.cancelSwitch(errUpstreamAttemptFirstOutputCanceled)
	w.cancelEvaluation()
	w.cancel(errUpstreamAttemptFirstOutputCanceled)
	return true
}

// disarm stops monitoring without canceling the in-flight upstream request.
// It is used when the administrator disables, removes, or replaces the group policy.
func (w *AccountLatencyRequestWatch) disarm() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.disarmed || w.completed || w.switched {
		w.mu.Unlock()
		return
	}
	w.disarmed = true
	w.stopTimersLocked()
	w.mu.Unlock()
	w.cancelEvaluation()
	w.monitor.unregisterRequestWatch(w)
	w.doneOnce.Do(func() { close(w.done) })
}

func (m *AccountLatencyMonitor) registerRequestWatch(watch *AccountLatencyRequestWatch) {
	if m == nil || watch == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.watches == nil {
		m.watches = make(map[int64]map[int64]map[*AccountLatencyRequestWatch]struct{})
	}
	byAccount := m.watches[watch.cfg.GroupID]
	if byAccount == nil {
		byAccount = make(map[int64]map[*AccountLatencyRequestWatch]struct{})
		m.watches[watch.cfg.GroupID] = byAccount
	}
	requests := byAccount[watch.accountID]
	if requests == nil {
		requests = make(map[*AccountLatencyRequestWatch]struct{})
		byAccount[watch.accountID] = requests
	}
	requests[watch] = struct{}{}
}

func (m *AccountLatencyMonitor) unregisterRequestWatch(watch *AccountLatencyRequestWatch) {
	if m == nil || watch == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byAccount := m.watches[watch.cfg.GroupID]
	if byAccount == nil {
		return
	}
	requests := byAccount[watch.accountID]
	delete(requests, watch)
	if len(requests) == 0 {
		delete(byAccount, watch.accountID)
	}
	if len(byAccount) == 0 {
		delete(m.watches, watch.cfg.GroupID)
	}
}

func (m *AccountLatencyMonitor) switchPendingRequestWatches(groupID, accountID, backupID int64, excluded map[int64]struct{}) {
	if m == nil || groupID <= 0 || accountID <= 0 || backupID <= 0 {
		return
	}
	m.mu.Lock()
	requests := m.watches[groupID][accountID]
	watches := make([]*AccountLatencyRequestWatch, 0, len(requests))
	for watch := range requests {
		watches = append(watches, watch)
	}
	m.mu.Unlock()

	for _, watch := range watches {
		watch.switchToBackup(backupID, excluded)
	}
}

func cloneAccountLatencyMonitorIDSet(values map[int64]struct{}) map[int64]struct{} {
	cloned := make(map[int64]struct{}, len(values))
	for accountID := range values {
		cloned[accountID] = struct{}{}
	}
	return cloned
}

func (w *AccountLatencyRequestWatch) Handled() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.deadlineObserved
}

func (w *AccountLatencyRequestWatch) RetryExcludedIDs() map[int64]struct{} {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[int64]struct{}, len(w.retryExcludedIDs))
	for accountID := range w.retryExcludedIDs {
		out[accountID] = struct{}{}
	}
	return out
}

func (w *AccountLatencyRequestWatch) stopTimersLocked() {
	if w.thresholdTimer != nil {
		w.thresholdTimer.Stop()
		w.thresholdTimer = nil
	}
	if w.immediateTimer != nil {
		w.immediateTimer.Stop()
		w.immediateTimer = nil
	}
}

func (w *AccountLatencyRequestWatch) allowFirstOutput(elapsed time.Duration) bool {
	return w.observeFirstToken(elapsed)
}

func (w *AccountLatencyRequestWatch) cancellationSignal() <-chan struct{} {
	if w == nil || w.switchCtx == nil {
		return nil
	}
	return w.switchCtx.Done()
}

func (w *AccountLatencyRequestWatch) cancellationError() error {
	if w == nil || w.switchCtx == nil {
		return nil
	}
	if err := context.Cause(w.switchCtx); errors.Is(err, errUpstreamAttemptFirstOutputCanceled) {
		return err
	}
	return nil
}

func (w *AccountLatencyRequestWatch) observationDone() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.done
}
