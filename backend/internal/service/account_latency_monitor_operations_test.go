package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAccountLatencyMonitorAlwaysEnabledThresholdTemporarilyDisablesAndRestores(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{{ID: 1, Schedulable: true}}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	cfg.AlwaysEnabledIDs = []int64{1}
	cfg.AlwaysDisableSec = 180
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})
	latency := (cfg.LatencyThresholdSec + 1) * 1000

	monitor.RecordRequest(context.Background(), cfg.GroupID, &repo.accounts[0], &latency, true)
	if !repo.accounts[0].Schedulable {
		t.Fatal("first threshold observation disabled the long-term enabled account too early")
	}
	monitor.RecordRequest(context.Background(), cfg.GroupID, &repo.accounts[0], &latency, true)
	if repo.accounts[0].Schedulable {
		t.Fatal("long-term enabled account remained schedulable after reaching the anomaly threshold")
	}
	monitor.mu.Lock()
	until := monitor.runtime[cfg.GroupID].temporaryDisabledUntil[1]
	monitor.runtime[cfg.GroupID].temporaryDisabledUntil[1] = time.Now().Add(-time.Second)
	monitor.mu.Unlock()
	if until.Sub(time.Now()) < 170*time.Second {
		t.Fatalf("temporary disable duration = %v, want about 180 seconds", until.Sub(time.Now()))
	}

	monitor.restoreTemporaryDisabledAccounts(context.Background())
	if !repo.accounts[0].Schedulable {
		t.Fatal("expired long-term account temporary disable was not restored")
	}
}

func TestAccountLatencyMonitorAlwaysEnabledImmediateThresholdDisablesOnFirstObservation(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{{ID: 1, Schedulable: true}}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	cfg.AlwaysEnabledIDs = []int64{1}
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})
	latency := (cfg.ImmediateThresholdSec + 1) * 1000

	monitor.RecordRequest(context.Background(), cfg.GroupID, &repo.accounts[0], &latency, true)
	if repo.accounts[0].Schedulable {
		t.Fatal("single-conversation timeout did not temporarily disable the long-term enabled account")
	}
}

func TestAccountLatencyMonitorRestoreWaitsForAccountTemporaryBlock(t *testing.T) {
	now := time.Now().UTC()
	rateLimitUntil := now.Add(2 * time.Minute)
	overloadUntil := now.Add(4 * time.Minute)
	tempUnschedulableUntil := now.Add(3 * time.Minute)
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{{
		ID:                     1,
		Status:                 StatusActive,
		Schedulable:            false,
		RateLimitResetAt:       &rateLimitUntil,
		OverloadUntil:          &overloadUntil,
		TempUnschedulableUntil: &tempUnschedulableUntil,
	}}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	cfg.AlwaysEnabledIDs = []int64{1}
	monitor := &AccountLatencyMonitor{
		accountRepo: repo,
		runtime: map[int64]*accountLatencyMonitorGroupRuntime{
			7: {temporaryDisabledUntil: map[int64]time.Time{1: now.Add(-time.Second)}},
		},
	}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})

	monitor.restoreTemporaryDisabledAccounts(context.Background())
	if repo.accounts[0].Schedulable {
		t.Fatal("account-level temporary block was bypassed during restore")
	}
	if len(repo.setCalls) != 0 {
		t.Fatalf("restore changed scheduling during account-level block: %#v", repo.setCalls)
	}
	if repo.accounts[0].Status != StatusActive || repo.accounts[0].RateLimitResetAt == nil ||
		!repo.accounts[0].RateLimitResetAt.Equal(rateLimitUntil) || repo.accounts[0].OverloadUntil == nil ||
		!repo.accounts[0].OverloadUntil.Equal(overloadUntil) || repo.accounts[0].TempUnschedulableUntil == nil ||
		!repo.accounts[0].TempUnschedulableUntil.Equal(tempUnschedulableUntil) {
		t.Fatalf("restore changed account business state: %#v", repo.accounts[0])
	}

	monitor.mu.Lock()
	retryAt := monitor.runtime[cfg.GroupID].temporaryDisabledUntil[1]
	monitor.mu.Unlock()
	if retryAt.Before(overloadUntil) {
		t.Fatalf("restore retry deadline = %v, want at least %v", retryAt, overloadUntil)
	}

	// Once the external account block expires, the monitor may restore only the
	// scheduling switch. The account status and block fields remain untouched.
	repo.accounts[0].RateLimitResetAt = nil
	expired := now.Add(-time.Second)
	repo.accounts[0].OverloadUntil = &expired
	repo.accounts[0].TempUnschedulableUntil = &expired
	monitor.mu.Lock()
	monitor.runtime[cfg.GroupID].temporaryDisabledUntil[1] = expired
	monitor.mu.Unlock()
	monitor.restoreTemporaryDisabledAccounts(context.Background())
	if !repo.accounts[0].Schedulable {
		t.Fatal("expired account-level temporary block did not restore scheduling")
	}
	if len(repo.setCalls) != 1 || repo.setCalls[0] != 1 {
		t.Fatalf("restore scheduling calls = %#v, want one enable", repo.setCalls)
	}
}

func TestAccountLatencyMonitorTemporaryBlockUntilUsesLatestExpiry(t *testing.T) {
	now := time.Now().UTC()
	rateLimitUntil := now.Add(time.Minute)
	overloadUntil := now.Add(3 * time.Minute)
	tempUnschedulableUntil := now.Add(2 * time.Minute)
	got := accountLatencyMonitorTemporaryBlockUntil(Account{
		RateLimitResetAt:       &rateLimitUntil,
		OverloadUntil:          &overloadUntil,
		TempUnschedulableUntil: &tempUnschedulableUntil,
	})
	if !got.Equal(overloadUntil) {
		t.Fatalf("temporary block deadline = %v, want %v", got, overloadUntil)
	}
}

func TestAccountLatencyMonitorRebalanceDoesNotReenableTemporarilyDisabledAlwaysAccount(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Schedulable: false},
		{ID: 2, Schedulable: true},
	}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	cfg.AlwaysEnabledIDs = []int64{1}
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: map[int64]*accountLatencyMonitorGroupRuntime{
		7: {temporaryDisabledUntil: map[int64]time.Time{1: time.Now().Add(time.Minute)}},
	}}

	if err := monitor.setSchedulableAccounts(context.Background(), cfg, []int64{2}, "test", "test"); err != nil {
		t.Fatalf("rebalance accounts: %v", err)
	}
	if repo.accounts[0].Schedulable {
		t.Fatal("rebalance re-enabled a temporarily disabled long-term account")
	}
}

func TestAccountLatencyMonitorActivateBestAccountsUsesLatestFullProbe(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Priority: 20, Schedulable: true},
		{ID: 2, Priority: 5, Schedulable: false},
		{ID: 3, Priority: 6, Schedulable: false},
		{ID: 4, Priority: 1, Schedulable: true},
	}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	cfg.AlwaysEnabledIDs = []int64{4}
	cfg.ActiveAccountCount = 1
	cfg.BackupCount = 1
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})
	probeSignature := accountLatencyMonitorFullProbeSignatureValue(cfg, repo.accounts)
	monitor.runtime[7] = &accountLatencyMonitorGroupRuntime{latestFullProbeResults: []accountLatencyProbeResult{
		{account: repo.accounts[0], latency: 1_000, success: true},
		{account: repo.accounts[1], latency: 2_000, success: true},
		{account: repo.accounts[2], latency: 3_000, success: true},
		{account: repo.accounts[3], latency: 500, success: true},
	}, latestFullProbeSignature: probeSignature}

	if err := monitor.ActivateBestAccounts(context.Background(), cfg.GroupID); err != nil {
		t.Fatalf("activate best accounts: %v", err)
	}
	if repo.accounts[0].Schedulable || !repo.accounts[1].Schedulable || repo.accounts[2].Schedulable || !repo.accounts[3].Schedulable {
		t.Fatalf("unexpected scheduling state: %#v", repo.accounts)
	}
	if got := accountIDsString(monitor.backupIDs(cfg.GroupID)); got != "3" {
		t.Fatalf("backup accounts = %s, want 3", got)
	}
}

func TestAccountLatencyMonitorActivateBestAccountsRequiresCompleteProbe(t *testing.T) {
	cfg := DefaultAccountLatencyMonitorGroup(7)
	monitor := &AccountLatencyMonitor{runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})

	err := monitor.ActivateBestAccounts(context.Background(), cfg.GroupID)
	if !errors.Is(err, ErrAccountLatencyMonitorNoProbeResult) {
		t.Fatalf("activate without probe error = %v, want ErrAccountLatencyMonitorNoProbeResult", err)
	}
}

func TestAccountLatencyMonitorActivateBestAccountsExcludesAccountLevelTemporaryBlock(t *testing.T) {
	blockedUntil := time.Now().Add(time.Minute)
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Priority: 20, Schedulable: true},
		{ID: 2, Priority: 1, Schedulable: false, TempUnschedulableUntil: &blockedUntil},
		{ID: 3, Priority: 5, Schedulable: false},
	}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	cfg.ActiveAccountCount = 1
	cfg.BackupCount = 1
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})
	probeSignature := accountLatencyMonitorFullProbeSignatureValue(cfg, repo.accounts)
	monitor.runtime[7] = &accountLatencyMonitorGroupRuntime{latestFullProbeResults: []accountLatencyProbeResult{
		{account: repo.accounts[0], latency: 3_000, success: true},
		{account: repo.accounts[1], latency: 1_000, success: true},
		{account: repo.accounts[2], latency: 2_000, success: true},
	}, latestFullProbeSignature: probeSignature}

	if err := monitor.ActivateBestAccounts(context.Background(), cfg.GroupID); err != nil {
		t.Fatalf("activate best accounts: %v", err)
	}
	if repo.accounts[1].Schedulable || !repo.accounts[2].Schedulable {
		t.Fatalf("account-level blocked account was selected: %#v", repo.accounts)
	}
}

func TestAccountLatencyMonitorActivateBestAccountsRejectsStaleAccountSet(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Priority: 20, Schedulable: true},
		{ID: 2, Priority: 10, Schedulable: false},
	}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})
	monitor.runtime[7] = &accountLatencyMonitorGroupRuntime{
		latestFullProbeResults: []accountLatencyProbeResult{
			{account: repo.accounts[0], latency: 2_000, success: true},
			{account: repo.accounts[1], latency: 1_000, success: true},
		},
		latestFullProbeSignature: accountLatencyMonitorFullProbeSignatureValue(cfg, repo.accounts),
	}
	repo.accounts = append(repo.accounts, Account{ID: 3, Priority: 1, Schedulable: false})

	err := monitor.ActivateBestAccounts(context.Background(), cfg.GroupID)
	if !errors.Is(err, ErrAccountLatencyMonitorNoProbeResult) {
		t.Fatalf("activate stale probe error = %v, want ErrAccountLatencyMonitorNoProbeResult", err)
	}
	if len(repo.setCalls) != 0 {
		t.Fatalf("stale probe changed scheduling: %#v", repo.setCalls)
	}
}

func TestAccountLatencyMonitorUpdateSettingsInvalidatesFullProbe(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{{ID: 1, Schedulable: true}}}
	settingRepo := &accountLatencyMonitorSettingRepoStub{}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	monitor := &AccountLatencyMonitor{
		accountRepo: repo,
		settingRepo: settingRepo,
		groupRepo:   &accountLatencyMonitorGroupRepoStub{},
		runtime: map[int64]*accountLatencyMonitorGroupRuntime{
			7: {
				latestFullProbeResults:   []accountLatencyProbeResult{{account: repo.accounts[0], success: true}},
				latestFullProbeSignature: "old",
			},
		},
	}
	settings := &AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}}
	if err := monitor.UpdateSettings(context.Background(), settings); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	monitor.mu.Lock()
	rt := monitor.runtime[cfg.GroupID]
	monitor.mu.Unlock()
	if len(rt.latestFullProbeResults) != 0 || rt.latestFullProbeSignature != "" {
		t.Fatal("settings update retained a stale complete probe")
	}
}

type blockingAccountLatencyMonitorRepo struct {
	*accountLatencyMonitorRepoStub
	restoreStarted chan struct{}
	allowRestore   chan struct{}
}

func (r *blockingAccountLatencyMonitorRepo) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	if schedulable {
		close(r.restoreStarted)
		select {
		case <-r.allowRestore:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.accountLatencyMonitorRepoStub.SetSchedulable(ctx, id, schedulable)
}

func TestAccountLatencyMonitorRestoreCannotOverrideNewTemporaryDisable(t *testing.T) {
	baseRepo := &accountLatencyMonitorRepoStub{accounts: []Account{{ID: 1, Schedulable: false}}}
	repo := &blockingAccountLatencyMonitorRepo{
		accountLatencyMonitorRepoStub: baseRepo,
		restoreStarted:                make(chan struct{}),
		allowRestore:                  make(chan struct{}),
	}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	cfg.AlwaysEnabledIDs = []int64{1}
	monitor := &AccountLatencyMonitor{
		accountRepo: repo,
		runtime: map[int64]*accountLatencyMonitorGroupRuntime{
			7: {temporaryDisabledUntil: map[int64]time.Time{1: time.Now().Add(-time.Second)}},
		},
	}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})

	restoreDone := make(chan struct{})
	go func() {
		monitor.restoreTemporaryDisabledAccounts(context.Background())
		close(restoreDone)
	}()
	select {
	case <-repo.restoreStarted:
	case <-time.After(time.Second):
		t.Fatal("restore did not start")
	}
	disableDone := make(chan struct{})
	go func() {
		monitor.temporarilyDisableAlwaysEnabledAccount(context.Background(), cfg, 1, "test", "test")
		close(disableDone)
	}()
	close(repo.allowRestore)
	select {
	case <-restoreDone:
	case <-time.After(time.Second):
		t.Fatal("restore did not finish")
	}
	select {
	case <-disableDone:
	case <-time.After(time.Second):
		t.Fatal("new temporary disable did not finish")
	}
	if baseRepo.accounts[0].Schedulable {
		t.Fatal("stale restore overwrote the newer temporary disable")
	}
	monitor.mu.Lock()
	until := monitor.runtime[cfg.GroupID].temporaryDisabledUntil[1]
	monitor.mu.Unlock()
	if !until.After(time.Now()) {
		t.Fatalf("new temporary disable was not retained: %v", until)
	}
}

type accountLatencyProbeRepoStub struct {
	*accountLatencyMonitorRepoStub
}

type accountLatencyMonitorGroupRepoStub struct {
	GroupRepository
}

func (r *accountLatencyMonitorGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	return &Group{ID: id}, nil
}

func (r *accountLatencyProbeRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, ErrAccountNotFound
}

func TestAccountLatencyMonitorProbeGroupProbesEveryAccount(t *testing.T) {
	repo := &accountLatencyProbeRepoStub{accountLatencyMonitorRepoStub: &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Priority: 20, Schedulable: true, Extra: map[string]any{"synthetic_ui_test": true}},
		{ID: 2, Priority: 10, Schedulable: false, Extra: map[string]any{"synthetic_ui_test": true}},
	}}}
	cfg := DefaultAccountLatencyMonitorGroup(7)
	monitor := &AccountLatencyMonitor{
		accountRepo: repo,
		tester:      &AccountTestService{accountRepo: repo},
		runtime:     make(map[int64]*accountLatencyMonitorGroupRuntime),
	}
	monitor.cacheSettings(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})

	if err := monitor.ProbeGroup(context.Background(), cfg.GroupID); err != nil {
		t.Fatalf("probe group: %v", err)
	}
	monitor.mu.Lock()
	rt := monitor.runtime[cfg.GroupID]
	resultCount := len(rt.latestFullProbeResults)
	accountCount := len(rt.accounts)
	lastProbe := rt.lastProbe
	monitor.mu.Unlock()
	if resultCount != 2 || accountCount != 2 {
		t.Fatalf("probe results/accounts = (%d, %d), want (2, 2)", resultCount, accountCount)
	}
	if lastProbe.IsZero() {
		t.Fatal("manual probe did not update the last probe time")
	}
}
