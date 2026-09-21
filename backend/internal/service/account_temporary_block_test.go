package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountLatencyMonitorTemporaryBlockSwitchesCurrentAccount(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Priority: 10, Schedulable: true},
		{ID: 2, Priority: 20, Schedulable: false},
	}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	monitor := &AccountLatencyMonitor{
		accountRepo: repo,
		runtime: map[int64]*accountLatencyMonitorGroupRuntime{
			7: {
				accounts: map[int64]*AccountLatencyMonitorAccountState{
					2: {AccountID: 2, LastSuccess: accountLatencyMonitorBoolPtr(true), LastLatencyMs: accountLatencyMonitorInt64Ptr(1_000)},
				},
				backups:      []int64{2},
				activeSince:  map[int64]time.Time{1: time.Now()},
				issues:       make(map[int64]map[string][]time.Time),
				recentIssues: make(map[int64][]time.Time),
			},
		},
	}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})

	monitor.handleAccountTemporaryBlock(1, time.Now().Add(time.Minute), "temp_unschedulable")
	monitor.mu.Lock()
	done := monitor.runtime[7].switchDone
	monitor.mu.Unlock()
	if done == nil {
		t.Fatal("temporary block did not start an immediate switch")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("temporary block switch did not finish")
	}

	if repo.accounts[0].Schedulable || !repo.accounts[1].Schedulable {
		t.Fatalf("unexpected scheduling state: %#v", repo.accounts)
	}
	monitor.mu.Lock()
	state := cloneAccountLatencyMonitorState(*monitor.runtime[7].accounts[1])
	history := append([]AccountLatencyMonitorSwitchRecord(nil), monitor.runtime[7].switchHistory...)
	monitor.mu.Unlock()
	if state.ConsecutiveFailure != 1 || state.RecentIssueCount != 1 {
		t.Fatalf("temporary block issue counts = (%d, %d), want (1, 1)", state.ConsecutiveFailure, state.RecentIssueCount)
	}
	if state.LastSuccess == nil || *state.LastSuccess {
		t.Fatalf("temporary block last success = %v, want false", state.LastSuccess)
	}
	if len(history) != 1 || history[0].ReasonCode != "temporary_block" {
		t.Fatalf("switch history = %#v, want one temporary_block record", history)
	}
	if !strings.Contains(history[0].Reason, "temp_unschedulable") {
		t.Fatalf("switch reason = %q, want block source", history[0].Reason)
	}
}

func TestAccountLatencyMonitorTemporaryBlockIgnoresNonCurrentAndDisabledGroups(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Schedulable: true},
		{ID: 2, Schedulable: false},
	}}
	enabled := DefaultAccountLatencyMonitorGroup(7)
	disabled := DefaultAccountLatencyMonitorGroup(8)
	disabled.Enabled = false
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{enabled, disabled}})

	monitor.handleAccountTemporaryBlock(2, time.Now().Add(time.Minute), "rate_limited")

	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if len(monitor.runtime) != 0 {
		t.Fatalf("ignored temporary block created runtime state: %#v", monitor.runtime)
	}
	if len(repo.setCalls) != 0 {
		t.Fatalf("ignored temporary block changed scheduling: %#v", repo.setCalls)
	}
}

type blockingTemporaryBlockSwitchRepo struct {
	*accountLatencyMonitorRepoStub
	switchStarted chan struct{}
	allowSwitch   chan struct{}
}

func (r *blockingTemporaryBlockSwitchRepo) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	if id == 1 && !schedulable {
		close(r.switchStarted)
		select {
		case <-r.allowSwitch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.accountLatencyMonitorRepoStub.SetSchedulable(ctx, id, schedulable)
}

func TestAccountLatencyMonitorTemporaryBlockWaitsForCurrentSwitch(t *testing.T) {
	baseRepo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Priority: 10, Schedulable: true},
		{ID: 2, Priority: 20, Schedulable: false},
	}}
	repo := &blockingTemporaryBlockSwitchRepo{
		accountLatencyMonitorRepoStub: baseRepo,
		switchStarted:                 make(chan struct{}),
		allowSwitch:                   make(chan struct{}),
	}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	initialSwitchDone := make(chan struct{})
	monitor := &AccountLatencyMonitor{
		accountRepo: repo,
		runtime: map[int64]*accountLatencyMonitorGroupRuntime{
			7: {
				accounts: map[int64]*AccountLatencyMonitorAccountState{
					2: {AccountID: 2, LastSuccess: accountLatencyMonitorBoolPtr(true), LastLatencyMs: accountLatencyMonitorInt64Ptr(1_000)},
				},
				backups:      []int64{2},
				switching:    true,
				switchDone:   initialSwitchDone,
				activeSince:  map[int64]time.Time{1: time.Now()},
				issues:       make(map[int64]map[string][]time.Time),
				recentIssues: make(map[int64][]time.Time),
			},
		},
	}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})

	monitor.handleAccountTemporaryBlock(1, time.Now().Add(time.Minute), "temp_unschedulable")
	monitor.mu.Lock()
	state := cloneAccountLatencyMonitorState(*monitor.runtime[7].accounts[1])
	monitor.mu.Unlock()
	if state.RecentIssueCount != 1 {
		t.Fatalf("queued temporary block issue count = %d, want 1", state.RecentIssueCount)
	}

	monitor.finishSwitch(cfg.GroupID)
	select {
	case <-repo.switchStarted:
	case <-time.After(time.Second):
		t.Fatal("temporary block was dropped while another switch was in progress")
	}
	monitor.mu.Lock()
	queuedSwitchDone := monitor.runtime[cfg.GroupID].switchDone
	monitor.mu.Unlock()
	if queuedSwitchDone == nil {
		t.Fatal("queued temporary block did not own the switch state")
	}
	close(repo.allowSwitch)
	select {
	case <-queuedSwitchDone:
	case <-time.After(time.Second):
		t.Fatal("queued temporary block switch did not finish")
	}
	if baseRepo.accounts[0].Schedulable || !baseRepo.accounts[1].Schedulable {
		t.Fatalf("queued temporary block did not switch accounts: %#v", baseRepo.accounts)
	}
}

type temporaryBlockObserverRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *temporaryBlockObserverRecorder) ObserveAccountTemporaryBlock(_ int64, _ time.Time, reason string) {
	r.mu.Lock()
	r.events = append(r.events, reason)
	r.mu.Unlock()
}

func (r *temporaryBlockObserverRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func TestOpenAIRuntimeBlockNotifiesOnlyWhenBlockChanges(t *testing.T) {
	recorder := &temporaryBlockObserverRecorder{}
	setAccountTemporaryBlockObserver(recorder)
	defer clearAccountTemporaryBlockObserver(recorder)
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 11, Platform: PlatformOpenAI}
	until := time.Now().Add(time.Minute)

	svc.BlockAccountScheduling(account, until, "first")
	svc.BlockAccountScheduling(account, until.Add(-time.Second), "shorter")

	if got := recorder.count(); got != 1 {
		t.Fatalf("temporary block notifications = %d, want 1", got)
	}
}

func TestAccountTemporaryBlockNotificationRequiresFutureExpiry(t *testing.T) {
	recorder := &temporaryBlockObserverRecorder{}
	setAccountTemporaryBlockObserver(recorder)
	defer clearAccountTemporaryBlockObserver(recorder)

	NotifyAccountTemporaryBlock(11, time.Time{}, "permanent_disable")
	NotifyAccountTemporaryBlock(11, time.Now().Add(-time.Second), "expired")
	NotifyAccountTemporaryBlock(0, time.Now().Add(time.Minute), "invalid_account")

	if got := recorder.count(); got != 0 {
		t.Fatalf("non-temporary block notifications = %d, want 0", got)
	}

	NotifyAccountTemporaryBlock(11, time.Now().Add(time.Minute), "temporary")
	if got := recorder.count(); got != 1 {
		t.Fatalf("temporary block notifications = %d, want 1", got)
	}
}

func TestOpenAIModelTransientDoesNotNotifyAccountTemporaryBlock(t *testing.T) {
	recorder := &temporaryBlockObserverRecorder{}
	setAccountTemporaryBlockObserver(recorder)
	defer clearAccountTemporaryBlockObserver(recorder)
	svc := &OpenAIGatewayService{openaiModelTransient: newOpenAIAccountModelTransientState(10)}
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	now := time.Now()

	svc.recordOpenAIAccountModelTransientFailure(account, "gpt-5.6-sol", now)
	if got := recorder.count(); got != 0 {
		t.Fatalf("first transient failure notifications = %d, want 0", got)
	}
	svc.recordOpenAIAccountModelTransientFailure(account, "gpt-5.6-sol", now.Add(time.Second))
	if got := recorder.count(); got != 0 {
		t.Fatalf("model-scoped cooldown notifications = %d, want 0", got)
	}
}
