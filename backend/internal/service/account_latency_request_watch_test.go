package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAccountLatencyRequestWatch_FirstTokenUsesElapsedDeadline(t *testing.T) {
	monitor := &AccountLatencyMonitor{runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	watch := newAccountLatencyRequestWatchForTest(monitor, cfg, 1, time.Now().Add(-11*time.Second))

	if !watch.observeFirstToken(11 * time.Second) {
		t.Fatal("first request above the ordinary threshold should continue when the switch count is not reached")
	}
	watch.mu.Lock()
	thresholdChecked := watch.thresholdChecked
	deadlineObserved := watch.deadlineObserved
	watch.mu.Unlock()
	if !thresholdChecked || !deadlineObserved {
		t.Fatal("elapsed first-token time did not synchronously evaluate the threshold")
	}

	monitor.mu.Lock()
	issues := len(monitor.ensureRuntimeLocked(cfg.GroupID).recentIssues[1])
	recordedLatency := monitor.ensureRuntimeLocked(cfg.GroupID).accounts[1].LastLatencyMs
	monitor.mu.Unlock()
	if issues != 1 {
		t.Fatalf("recent issue count = %d, want 1", issues)
	}
	if recordedLatency == nil || *recordedLatency < 11_000 {
		t.Fatalf("recorded latency = %v, want actual elapsed latency at least 11000ms", recordedLatency)
	}
	watch.Complete(accountLatencyMonitorIntPtr(11_000), true)
}

func TestAccountLatencyRequestWatch_SwitchCancelsAllPendingRequests(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Priority: 10, Schedulable: true},
		{ID: 2, Priority: 20, Schedulable: false},
	}}
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	monitor.runtime[7] = &accountLatencyMonitorGroupRuntime{
		accounts: map[int64]*AccountLatencyMonitorAccountState{
			2: {AccountID: 2, LastSuccess: accountLatencyMonitorBoolPtr(true), LastLatencyMs: accountLatencyMonitorInt64Ptr(1_000)},
		},
		backups:      []int64{2},
		activeSince:  map[int64]time.Time{1: time.Now()},
		issues:       make(map[int64]map[string][]time.Time),
		recentIssues: make(map[int64][]time.Time),
	}

	startedAt := time.Now().Add(-41 * time.Second)
	first := newAccountLatencyRequestWatchForTest(monitor, cfg, 1, startedAt)
	second := newAccountLatencyRequestWatchForTest(monitor, cfg, 1, startedAt)
	if first.observeFirstToken(41 * time.Second) {
		t.Fatal("the request that crosses the immediate deadline must not emit its first token")
	}
	for name, watch := range map[string]*AccountLatencyRequestWatch{"first": first, "second": second} {
		select {
		case <-watch.ctx.Done():
		case <-time.After(time.Second):
			t.Fatalf("%s pending request was not canceled", name)
		}
		failoverErr := watch.Complete(nil, false)
		if failoverErr == nil {
			t.Fatalf("%s pending request did not request standby replay", name)
		}
		if !failoverErr.SafeToFailoverAfterWrite {
			t.Fatalf("%s failover must remain replayable after non-semantic stream bytes", name)
		}
		if _, excluded := watch.RetryExcludedIDs()[2]; excluded {
			t.Fatalf("%s request excluded selected backup account 2", name)
		}
	}
	if repo.accounts[0].Schedulable || !repo.accounts[1].Schedulable {
		t.Fatalf("unexpected scheduling state: %#v", repo.accounts)
	}
}

func TestAccountLatencyRequestWatch_DetachedAttemptStillCancelsAfterParentCancellation(t *testing.T) {
	monitor := &AccountLatencyMonitor{runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	parent, cancelParent := context.WithCancel(context.Background())
	watch := newAccountLatencyRequestWatchForTestWithParent(monitor, cfg, 1, time.Now(), parent)
	detached := detachContextWithUpstreamAttemptCancellation(watch.ctx)

	cancelParent()
	select {
	case <-detached.Done():
		t.Fatalf("detached attempt followed client cancellation: %v", context.Cause(detached))
	case <-time.After(20 * time.Millisecond):
	}

	if !watch.switchToBackup(2, map[int64]struct{}{1: {}}) {
		t.Fatal("monitor switch was not applied")
	}
	select {
	case <-detached.Done():
		if !errors.Is(context.Cause(detached), errUpstreamAttemptFirstOutputCanceled) {
			t.Fatalf("detached attempt cancellation cause = %v", context.Cause(detached))
		}
	case <-time.After(time.Second):
		t.Fatal("detached attempt was not canceled by the monitor switch")
	}
}

func TestDetachBillingPersistenceContextIgnoresMonitorSwitch(t *testing.T) {
	monitor := &AccountLatencyMonitor{runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	watch := newAccountLatencyRequestWatchForTest(monitor, DefaultAccountLatencyMonitorGroup(7), 1, time.Now())
	billingCtx := detachBillingPersistenceContext(watch.ctx)

	if !watch.switchToBackup(2, nil) {
		t.Fatal("monitor switch was not applied")
	}
	if err := billingCtx.Err(); err != nil {
		t.Fatalf("billing persistence context was canceled by account switch: %v", err)
	}
}

func TestAccountLatencyMonitorResetRuntimeDisarmsDisabledGroupWatches(t *testing.T) {
	monitor := &AccountLatencyMonitor{
		runtime: make(map[int64]*accountLatencyMonitorGroupRuntime),
		watches: make(map[int64]map[int64]map[*AccountLatencyRequestWatch]struct{}),
	}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	watch := newAccountLatencyRequestWatchForTest(monitor, cfg, 1, time.Now())

	cfg.Enabled = false
	monitor.resetRuntimeForSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})

	if !watch.allowFirstOutput(time.Duration(cfg.ImmediateThresholdSec) * time.Second) {
		t.Fatal("disabled monitor blocked an in-flight response")
	}
	if err := watch.ctx.Err(); err != nil {
		t.Fatalf("disabling monitoring canceled the upstream request: %v", err)
	}
	select {
	case <-watch.observationDone():
	default:
		t.Fatal("disabled watch did not release its observer")
	}
	select {
	case <-watch.evaluationCtx.Done():
	default:
		t.Fatal("disabled watch did not cancel its pending evaluation")
	}
	monitor.mu.Lock()
	_, registered := monitor.watches[cfg.GroupID]
	_, runtimeExists := monitor.runtime[cfg.GroupID]
	monitor.mu.Unlock()
	if registered || runtimeExists {
		t.Fatalf("disabled group retained state: registered=%t runtime=%t", registered, runtimeExists)
	}
}

func TestAccountLatencyMonitorResetRuntimeDisarmsWatchesUsingOldEnabledSettings(t *testing.T) {
	monitor := &AccountLatencyMonitor{
		runtime: make(map[int64]*accountLatencyMonitorGroupRuntime),
		watches: make(map[int64]map[int64]map[*AccountLatencyRequestWatch]struct{}),
	}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	watch := newAccountLatencyRequestWatchForTest(monitor, cfg, 1, time.Now())
	cfg.LatencyThresholdSec++

	monitor.resetRuntimeForSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})

	if !watch.allowFirstOutput(time.Duration(cfg.ImmediateThresholdSec) * time.Second) {
		t.Fatal("watch using old settings blocked an in-flight response")
	}
	select {
	case <-watch.observationDone():
	default:
		t.Fatal("watch using old settings was not disarmed")
	}
}

func TestAccountLatencyMonitorHealthyBackup_ExcludesAlreadyScheduledAccount(t *testing.T) {
	fast := int64(1_000)
	other := int64(2_000)
	monitor := &AccountLatencyMonitor{runtime: map[int64]*accountLatencyMonitorGroupRuntime{
		7: {
			accounts: map[int64]*AccountLatencyMonitorAccountState{
				2: {AccountID: 2, LastSuccess: accountLatencyMonitorBoolPtr(true), LastLatencyMs: &fast},
				3: {AccountID: 3, LastSuccess: accountLatencyMonitorBoolPtr(true), LastLatencyMs: &other},
			},
			backups:      []int64{2, 3},
			recentIssues: make(map[int64][]time.Time),
		},
	}}
	accounts := []Account{
		{ID: 1, Priority: 10, Schedulable: true},
		{ID: 2, Priority: 1, Schedulable: true},
		{ID: 3, Priority: 20, Schedulable: false},
	}

	backupID, ok := monitor.healthyBackup(DefaultAccountLatencyMonitorGroup(7), 1, accounts)
	if !ok || backupID != 3 {
		t.Fatalf("healthy backup = (%d, %t), want (3, true)", backupID, ok)
	}
}

func TestAccountLatencyMonitorHealthyBackup_RemovesAccountOutsideGroup(t *testing.T) {
	latency := int64(1_000)
	monitor := &AccountLatencyMonitor{runtime: map[int64]*accountLatencyMonitorGroupRuntime{
		7: {
			accounts: map[int64]*AccountLatencyMonitorAccountState{
				2: {AccountID: 2, LastSuccess: accountLatencyMonitorBoolPtr(true), LastLatencyMs: &latency},
				3: {AccountID: 3, LastSuccess: accountLatencyMonitorBoolPtr(true), LastLatencyMs: &latency},
			},
			backups:      []int64{2, 3},
			recentIssues: make(map[int64][]time.Time),
		},
	}}
	accounts := []Account{
		{ID: 1, Priority: 10, Schedulable: true},
		{ID: 3, Priority: 20, Schedulable: false},
	}

	backupID, ok := monitor.healthyBackup(DefaultAccountLatencyMonitorGroup(7), 1, accounts)
	if !ok || backupID != 3 {
		t.Fatalf("healthy backup = (%d, %t), want (3, true)", backupID, ok)
	}
	if got := accountIDsString(monitor.backupIDs(7)); got != "3" {
		t.Fatalf("stored backup IDs = %s, want 3", got)
	}
}

func newAccountLatencyRequestWatchForTest(monitor *AccountLatencyMonitor, cfg AccountLatencyMonitorGroup, accountID int64, startedAt time.Time) *AccountLatencyRequestWatch {
	return newAccountLatencyRequestWatchForTestWithParent(monitor, cfg, accountID, startedAt, context.Background())
}

func newAccountLatencyRequestWatchForTestWithParent(monitor *AccountLatencyMonitor, cfg AccountLatencyMonitorGroup, accountID int64, startedAt time.Time, parent context.Context) *AccountLatencyRequestWatch {
	ctx, cancel := context.WithCancelCause(parent)
	switchCtx, cancelSwitch := context.WithCancelCause(context.Background())
	evaluationCtx, cancelEvaluation := context.WithCancel(context.Background())
	watch := &AccountLatencyRequestWatch{
		monitor:          monitor,
		cfg:              cfg,
		accountID:        accountID,
		startedAt:        startedAt,
		cancel:           cancel,
		switchCtx:        switchCtx,
		cancelSwitch:     cancelSwitch,
		evaluationCtx:    evaluationCtx,
		cancelEvaluation: cancelEvaluation,
		done:             make(chan struct{}),
	}
	watch.ctx = withUpstreamAttemptObserver(ctx, watch, watch.startedAt)
	monitor.registerRequestWatch(watch)
	return watch
}

func accountLatencyMonitorIntPtr(value int) *int {
	return &value
}
