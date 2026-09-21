package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestSortAccountLatencyMonitorStates_UsesFourLayers(t *testing.T) {
	fastSlow := int64(9_000)
	fastQuick := int64(1_000)
	slow := int64(11_000)
	states := []AccountLatencyMonitorAccountState{
		{AccountID: 1, AccountPriority: 0, LastLatencyMs: &slow},
		{AccountID: 2, AccountPriority: 5, RecentIssueCount: 0, LastLatencyMs: &fastQuick},
		{AccountID: 3, AccountPriority: 1, RecentIssueCount: 1, LastLatencyMs: &fastSlow},
		{AccountID: 4, AccountPriority: 1},
	}

	sortAccountLatencyMonitorStates(states, 10_000)
	got := accountIDsString([]int64{states[0].AccountID, states[1].AccountID, states[2].AccountID, states[3].AccountID})
	if want := "2,3,1,4"; got != want {
		t.Fatalf("sorted account IDs = %s, want %s", got, want)
	}
}

func TestSelectAccountLatencyMonitorBackups_PrefersFastThenAccountPriority(t *testing.T) {
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, Priority: 0}, latency: 12_000, success: true},
		{account: Account{ID: 2, Priority: 5}, latency: 8_000, success: true},
		{account: Account{ID: 3, Priority: 1}, latency: 9_000, success: true},
		{account: Account{ID: 4, Priority: 1}, latency: 7_000, success: false},
	}

	chosen := selectAccountLatencyMonitorBackups(results, 7, 10_000, 3, nil)
	if got, want := accountIDsString(chosen), "3,2,1"; got != want {
		t.Fatalf("selected backups = %s, want %s", got, want)
	}
}

func TestSelectAccountLatencyMonitorBackups_PrefersAccountPriorityWithinFastAccounts(t *testing.T) {
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, Priority: 4}, latency: 1_000, success: true},
		{account: Account{ID: 2, Priority: 1}, latency: 9_000, success: true},
		{account: Account{ID: 3, Priority: 1}, latency: 7_000, success: true},
	}

	chosen := selectAccountLatencyMonitorBackups(results, 7, 10_000, 3, nil)
	if got, want := accountIDsString(chosen), "3,2,1"; got != want {
		t.Fatalf("selected backups = %s, want %s", got, want)
	}
}

func TestSelectAccountLatencyMonitorBackups_PrefersFewerRecentIssuesBeforePriority(t *testing.T) {
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, Priority: 1}, latency: 2_000, success: true, recentIssueCount: 3},
		{account: Account{ID: 2, Priority: 50}, latency: 8_000, success: true, recentIssueCount: 0},
		{account: Account{ID: 3, Priority: 10}, latency: 6_000, success: true, recentIssueCount: 1},
	}

	chosen := selectAccountLatencyMonitorBackups(results, 7, 10_000, 3, nil)
	if got, want := accountIDsString(chosen), "2,3,1"; got != want {
		t.Fatalf("selected backups = %s, want %s", got, want)
	}
}

func TestSelectAccountLatencyMonitorBackups_UsesConfiguredThreeLayerOrder(t *testing.T) {
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, Priority: 100}, latency: 5_000, success: true},
		{account: Account{ID: 2, Priority: 99}, latency: 8_000, success: true},
		{account: Account{ID: 3, Priority: 150}, latency: 1_000, success: true},
		{account: Account{ID: 4, Priority: 30}, latency: 15_000, success: true},
	}

	candidates := selectAccountLatencyMonitorBackups(results, 7, 10_000, 4, nil)
	if got, want := accountIDsString(candidates), "2,1,3,4"; got != want {
		t.Fatalf("ordered candidates = %s, want %s", got, want)
	}
	currentIDs, backups := accountLatencyMonitorTargets(candidates, nil, 1, 2)
	if got, want := accountIDsString(currentIDs), "2"; got != want {
		t.Fatalf("current accounts = %s, want %s", got, want)
	}
	if got, want := accountIDsString(backups), "1,3"; got != want {
		t.Fatalf("backup accounts = %s, want %s", got, want)
	}
}

func TestSelectAccountLatencyMonitorBackups_SortsEqualPriorityByLatency(t *testing.T) {
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, Priority: 2}, latency: 8_000, success: true},
		{account: Account{ID: 2, Priority: 2}, latency: 6_000, success: true},
		{account: Account{ID: 3, Priority: 2}, latency: 7_000, success: true},
	}

	chosen := selectAccountLatencyMonitorBackups(results, 7, 10_000, 2, map[int64]struct{}{2: {}})
	if got, want := accountIDsString(chosen), "3,1"; got != want {
		t.Fatalf("selected backups = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorTargets_ExcludesCurrentAndAlwaysEnabledAccountsFromBackups(t *testing.T) {
	currentIDs, backups := accountLatencyMonitorTargets([]int64{1, 2, 3, 4}, []int64{2}, 1, 2)
	if got, want := accountIDsString(currentIDs), "1"; got != want {
		t.Fatalf("current accounts = %s, want %s", got, want)
	}
	if got, want := accountIDsString(backups), "3,4"; got != want {
		t.Fatalf("backup accounts = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorTargets_DoesNotUseAlwaysEnabledAccountAsCurrent(t *testing.T) {
	currentIDs, backups := accountLatencyMonitorTargets([]int64{1, 2, 3, 4}, []int64{1}, 1, 2)
	if got, want := accountIDsString(currentIDs), "2"; got != want {
		t.Fatalf("current accounts = %s, want %s", got, want)
	}
	if got, want := accountIDsString(backups), "3,4"; got != want {
		t.Fatalf("backup accounts = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorTargets_UsesConfiguredActiveAccountCount(t *testing.T) {
	currentIDs, backups := accountLatencyMonitorTargets([]int64{1, 2, 3, 4, 5}, []int64{2}, 2, 2)
	if got, want := accountIDsString(currentIDs), "1,3"; got != want {
		t.Fatalf("current accounts = %s, want %s", got, want)
	}
	if got, want := accountIDsString(backups), "4,5"; got != want {
		t.Fatalf("backup accounts = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorStandbyIDs_ExcludesActiveAndAlwaysEnabledAccounts(t *testing.T) {
	standbys := accountLatencyMonitorStandbyIDs([]int64{1, 2, 3, 2, 4}, []int64{1}, []int64{3})
	if got, want := accountIDsString(standbys), "2,4"; got != want {
		t.Fatalf("standby accounts = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorCurrentIDs_ExcludesAlwaysEnabledAccounts(t *testing.T) {
	accounts := []Account{
		{ID: 1, Schedulable: true},
		{ID: 2, Schedulable: true},
		{ID: 3, Schedulable: false},
	}
	if got, want := accountIDsString(accountLatencyMonitorCurrentIDs(accounts, []int64{1})), "2"; got != want {
		t.Fatalf("current account IDs = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorPeriodicSwitchAllowed_UsesCooldown(t *testing.T) {
	now := time.Now().UTC()
	if accountLatencyMonitorPeriodicSwitchAllowed(now.Add(-9*time.Minute), []int64{1}, 600, now) {
		t.Fatal("periodic switch should be blocked during cooldown")
	}
	if !accountLatencyMonitorPeriodicSwitchAllowed(now.Add(-10*time.Minute), []int64{1}, 600, now) {
		t.Fatal("periodic switch should be allowed when cooldown expires")
	}
	if !accountLatencyMonitorPeriodicSwitchAllowed(now, nil, 600, now) {
		t.Fatal("initial activation should be allowed without a current account")
	}
}

func TestAccountLatencyMonitorBackupIDsExcludingActive_RemovesStaleCurrentAccount(t *testing.T) {
	monitor := &AccountLatencyMonitor{runtime: map[int64]*accountLatencyMonitorGroupRuntime{
		7: {backups: []int64{1, 2, 3}},
	}}
	if got, want := accountIDsString(monitor.backupIDsExcludingActive(7, []int64{2})), "1,3"; got != want {
		t.Fatalf("backup IDs = %s, want %s", got, want)
	}
	if got, want := accountIDsString(monitor.backupIDs(7)), "1,3"; got != want {
		t.Fatalf("stored backup IDs = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorActiveAccountSwitchAllowed_UsesAccountStartTime(t *testing.T) {
	now := time.Now().UTC()
	monitor := &AccountLatencyMonitor{runtime: map[int64]*accountLatencyMonitorGroupRuntime{
		7: {activeSince: map[int64]time.Time{1: now.Add(-10 * time.Minute), 2: now.Add(-5 * time.Minute)}},
	}}
	if !monitor.activeAccountSwitchAllowed(7, 1, 600, now) {
		t.Fatal("account 1 should be eligible after its own switch period")
	}
	if monitor.activeAccountSwitchAllowed(7, 2, 600, now) {
		t.Fatal("account 2 should remain in its own switch period")
	}
}

func TestAccountLatencyMonitorSetSchedulableAccounts_RollsBackWhenEnableFails(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{
		accounts:   []Account{{ID: 1, Schedulable: true}, {ID: 2, Schedulable: false}},
		failEnable: 2,
	}
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}
	err := monitor.setSchedulableAccounts(context.Background(), AccountLatencyMonitorGroup{GroupID: 7}, []int64{2}, "test", "test")
	if err == nil {
		t.Fatal("expected enable failure")
	}
	if !repo.accounts[0].Schedulable || repo.accounts[1].Schedulable {
		t.Fatalf("accounts were not restored: %#v", repo.accounts)
	}
}

func TestAccountLatencyMonitorFailover_IgnoresResultFromNoLongerSchedulableAccount(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{
		accounts: []Account{
			{ID: 1, Schedulable: false},
			{ID: 2, Schedulable: true},
		},
	}
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}

	monitor.failover(context.Background(), AccountLatencyMonitorGroup{GroupID: 7, ActiveAccountCount: 1}, 1, "user_single_latency", "迟到结果")

	if len(repo.setCalls) != 0 {
		t.Fatalf("stale result changed scheduling: %#v", repo.setCalls)
	}
	if repo.accounts[0].Schedulable || !repo.accounts[1].Schedulable {
		t.Fatalf("scheduling changed: %#v", repo.accounts)
	}
}

func TestAccountLatencyMonitorGetRuntime_RemovesDeletedAccounts(t *testing.T) {
	settings := AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{
		DefaultAccountLatencyMonitorGroup(7),
	}}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 2, Priority: 20, Schedulable: true},
		{ID: 3, Priority: 30, Schedulable: false},
	}}
	now := time.Now().UTC()
	monitor := &AccountLatencyMonitor{
		settingRepo:    &accountLatencyMonitorSettingRepoStub{value: string(settingsJSON)},
		accountRepo:    repo,
		cachedSettings: settings,
		runtime: map[int64]*accountLatencyMonitorGroupRuntime{
			7: {
				accounts: map[int64]*AccountLatencyMonitorAccountState{
					1: {AccountID: 1},
					2: {AccountID: 2},
					3: {AccountID: 3},
				},
				backups:       []int64{1, 3},
				activeSince:   map[int64]time.Time{1: now, 2: now},
				issues:        map[int64]map[string][]time.Time{1: {accountLatencyMonitorIssueFailure: {now}}, 2: {}},
				recentIssues:  map[int64][]time.Time{1: {now}, 2: {now}},
				switchHistory: []AccountLatencyMonitorSwitchRecord{{PreviousAccountIDs: []int64{1}, CurrentAccountIDs: []int64{2}}},
			},
		},
	}

	states, err := monitor.GetRuntime(context.Background())
	if err != nil {
		t.Fatalf("get runtime: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("runtime groups = %d, want 1", len(states))
	}
	if len(states[0].Accounts) != 2 {
		t.Fatalf("runtime accounts = %d, want 2", len(states[0].Accounts))
	}
	priorities := make(map[int64]int, len(states[0].Accounts))
	for _, account := range states[0].Accounts {
		priorities[account.AccountID] = account.AccountPriority
	}
	if priorities[2] != 20 || priorities[3] != 30 {
		t.Fatalf("runtime account priorities = %#v, want map[2:20 3:30]", priorities)
	}
	if got, want := accountIDsString(states[0].BackupAccountIDs), "3"; got != want {
		t.Fatalf("backup account IDs = %s, want %s", got, want)
	}

	rt := monitor.runtime[7]
	if _, exists := rt.accounts[1]; exists {
		t.Fatal("deleted account remained in runtime account states")
	}
	if _, exists := rt.issues[1]; exists {
		t.Fatal("deleted account remained in runtime issue states")
	}
	if _, exists := rt.recentIssues[1]; exists {
		t.Fatal("deleted account remained in recent issue states")
	}
	if _, exists := rt.activeSince[1]; exists {
		t.Fatal("deleted account remained in active-since states")
	}
	if got, want := accountIDsString(rt.backups), "3"; got != want {
		t.Fatalf("stored backup account IDs = %s, want %s", got, want)
	}
	if len(states[0].SwitchHistory) != 1 {
		t.Fatal("historical switch records should be retained")
	}
}

type accountLatencyMonitorRepoStub struct {
	AccountRepository
	accounts   []Account
	failEnable int64
	setCalls   []int64
}

type accountLatencyMonitorSettingRepoStub struct {
	SettingRepository
	value string
}

func (r *accountLatencyMonitorSettingRepoStub) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}

func TestAccountLatencyMonitorRefreshSettingsPreservesInitialRuntimeAndDisarmsAfterChange(t *testing.T) {
	cfg := DefaultAccountLatencyMonitorGroup(7)
	payload, err := json.Marshal(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})
	if err != nil {
		t.Fatalf("marshal initial settings: %v", err)
	}
	repo := &accountLatencyMonitorSettingRepoStub{value: string(payload)}
	monitor := &AccountLatencyMonitor{
		settingRepo: repo,
		runtime: map[int64]*accountLatencyMonitorGroupRuntime{
			7: {backups: []int64{2}},
		},
		watches: make(map[int64]map[int64]map[*AccountLatencyRequestWatch]struct{}),
	}
	watch := newAccountLatencyRequestWatchForTest(monitor, cfg, 1, time.Now())

	monitor.refreshCachedSettings(context.Background())
	if got := accountIDsString(monitor.backupIDs(7)); got != "2" {
		t.Fatalf("initial settings load cleared restored backups: %s", got)
	}
	select {
	case <-watch.observationDone():
		t.Fatal("initial settings load disarmed an existing watch")
	default:
	}

	cfg.LatencyThresholdSec++
	payload, err = json.Marshal(AccountLatencyMonitorSettings{Groups: []AccountLatencyMonitorGroup{cfg}})
	if err != nil {
		t.Fatalf("marshal changed settings: %v", err)
	}
	repo.value = string(payload)
	monitor.refreshCachedSettings(context.Background())
	select {
	case <-watch.observationDone():
	default:
		t.Fatal("changed settings did not disarm the watch using old settings")
	}
}

func (r *accountLatencyMonitorRepoStub) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return append([]Account(nil), r.accounts...), nil
}

type deadlineAccountLatencyMonitorRepoStub struct {
	*accountLatencyMonitorRepoStub
	listHadDeadline bool
}

func (r *deadlineAccountLatencyMonitorRepoStub) ListAllWithFilters(ctx context.Context, _ string, _ string, _ string, _ string, _ int64, _ string) ([]Account, error) {
	_, r.listHadDeadline = ctx.Deadline()
	return append([]Account(nil), r.accounts...), nil
}

func (r *accountLatencyMonitorRepoStub) SetSchedulable(_ context.Context, id int64, schedulable bool) error {
	r.setCalls = append(r.setCalls, id)
	if schedulable && id == r.failEnable {
		r.failEnable = 0
		return errors.New("enable failed")
	}
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].Schedulable = schedulable
			return nil
		}
	}
	return errors.New("account not found")
}

func TestAccountLatencyMonitorPreferredCurrentID_ChoosesBestDynamicAccount(t *testing.T) {
	cfg := DefaultAccountLatencyMonitorGroup(1)
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, Priority: 100}, latency: 8_000, success: true},
		{account: Account{ID: 2, Priority: 10}, latency: 9_000, success: true},
	}
	if got := accountLatencyMonitorPreferredCurrentID(results, []int64{1, 2}, cfg, 10_000); got != 2 {
		t.Fatalf("preferred current account = %d, want 2", got)
	}
}

func TestNormalizeAccountLatencyMonitorGroup_AppliesDefaultsWithoutCappingBackupCount(t *testing.T) {
	cfg := AccountLatencyMonitorGroup{GroupID: 1, BackupCount: 3, AlwaysEnabledIDs: []int64{2, 2, -1, 4}}
	normalizeAccountLatencyMonitorGroup(&cfg)

	if cfg.LatencyThresholdSec != 10 || cfg.FailureWindowSec != 30 || cfg.ConsecutiveFailures != 2 {
		t.Fatalf("unexpected request defaults: %#v", cfg)
	}
	if cfg.ProbeIntervalSec != 60 || cfg.IdleProbeIntervalSec != 1800 || cfg.ProbeTimeoutSec != 30 || cfg.ProbeConcurrency != 4 || cfg.SwitchCooldownSec != 600 || cfg.ImmediateThresholdSec != 40 || cfg.RecentIssueWindowSec != 600 {
		t.Fatalf("unexpected probe defaults: %#v", cfg)
	}
	if cfg.BackupCount != 3 {
		t.Fatalf("backup_count = %d, want 3", cfg.BackupCount)
	}
	if cfg.ActiveAccountCount != 1 {
		t.Fatalf("active_account_count = %d, want 1", cfg.ActiveAccountCount)
	}
	if got, want := accountIDsString(cfg.AlwaysEnabledIDs), "2,4"; got != want {
		t.Fatalf("always enabled IDs = %s, want %s", got, want)
	}
	if cfg.ProbeModel != "gpt-5.6-sol" || cfg.ProbePrompt != "hi" || cfg.ProbeReasoning != "low" {
		t.Fatalf("unexpected probe defaults: %#v", cfg)
	}
}

func TestAccountLatencyMonitorProbeInterval_UsesIdleIntervalWithoutNewUserRequest(t *testing.T) {
	cfg := AccountLatencyMonitorGroup{ProbeIntervalSec: 60, IdleProbeIntervalSec: 1800}
	lastProbe := time.Now()
	rt := &accountLatencyMonitorGroupRuntime{lastProbe: lastProbe, lastUserRequest: lastProbe.Add(-time.Second)}
	if got, want := accountLatencyMonitorProbeInterval(cfg, rt), 30*time.Minute; got != want {
		t.Fatalf("idle probe interval = %s, want %s", got, want)
	}

	rt.lastUserRequest = lastProbe.Add(time.Second)
	if got, want := accountLatencyMonitorProbeInterval(cfg, rt), time.Minute; got != want {
		t.Fatalf("active probe interval = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorWindowIssueCount_KeepsAnomalyAcrossNormalRequest(t *testing.T) {
	rt := &accountLatencyMonitorGroupRuntime{issues: make(map[int64]map[string][]time.Time)}
	start := time.Now()
	count, total := accountLatencyMonitorWindowIssueCount(rt, 1, accountLatencyMonitorIssueLatency, start, 30*time.Second)
	if count != 1 || total != 1 {
		t.Fatalf("first latency anomaly = (%d, %d), want (1, 1)", count, total)
	}
	count, total = accountLatencyMonitorWindowIssueCount(rt, 1, "", start.Add(5*time.Second), 30*time.Second)
	if count != 0 || total != 1 {
		t.Fatalf("normal request = (%d, %d), want (0, 1)", count, total)
	}
	count, total = accountLatencyMonitorWindowIssueCount(rt, 1, accountLatencyMonitorIssueLatency, start.Add(10*time.Second), 30*time.Second)
	if count != 2 || total != 2 {
		t.Fatalf("second latency anomaly = (%d, %d), want (2, 2)", count, total)
	}
}

func TestAccountLatencyMonitorRecentIssueCount_UsesConfiguredWindow(t *testing.T) {
	rt := &accountLatencyMonitorGroupRuntime{recentIssues: make(map[int64][]time.Time)}
	start := time.Now()
	if got := accountLatencyMonitorRecentIssueCount(rt, 1, start, 90*time.Second, true); got != 1 {
		t.Fatalf("first recent issue count = %d, want 1", got)
	}
	if got := accountLatencyMonitorRecentIssueCount(rt, 1, start.Add(80*time.Second), 90*time.Second, true); got != 2 {
		t.Fatalf("second recent issue count = %d, want 2", got)
	}
	if got := accountLatencyMonitorRecentIssueCount(rt, 1, start.Add(100*time.Second), 90*time.Second, false); got != 1 {
		t.Fatalf("pruned recent issue count = %d, want 1", got)
	}
}

func TestTrimAccountLatencyMonitorSwitchHistory_KeepsLatestHundred(t *testing.T) {
	records := make([]AccountLatencyMonitorSwitchRecord, 150)
	for i := range records {
		records[i].Reason = string(rune(i))
	}
	trimmed := trimAccountLatencyMonitorSwitchHistory(records)
	if len(trimmed) != 100 {
		t.Fatalf("switch history length = %d, want 100", len(trimmed))
	}
	if trimmed[0].Reason != records[0].Reason || trimmed[99].Reason != records[99].Reason {
		t.Fatal("switch history did not retain the newest records")
	}
}

func TestAccountLatencyMonitorDeadlineSwitchesOnSecondThresholdWithoutWaitingForCompletion(t *testing.T) {
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

	if backupID, _ := monitor.recordFirstTokenDeadlineAndSwitch(context.Background(), cfg, 1, time.Duration(cfg.LatencyThresholdSec)*time.Second, false, true); backupID != 0 {
		t.Fatalf("first threshold selected backup %d, want no switch", backupID)
	}
	backupID, excluded := monitor.recordFirstTokenDeadlineAndSwitch(context.Background(), cfg, 1, time.Duration(cfg.LatencyThresholdSec)*time.Second, false, true)
	if backupID != 2 {
		t.Fatalf("second threshold selected backup %d, want 2", backupID)
	}
	if repo.accounts[0].Schedulable || !repo.accounts[1].Schedulable {
		t.Fatalf("unexpected scheduling state: %#v", repo.accounts)
	}
	if _, ok := excluded[1]; !ok {
		t.Fatalf("failed account missing from retry exclusions: %#v", excluded)
	}
}

func TestAccountLatencyMonitorTryActivateHealthyBackupUsesDeadline(t *testing.T) {
	repo := &deadlineAccountLatencyMonitorRepoStub{accountLatencyMonitorRepoStub: &accountLatencyMonitorRepoStub{
		accounts: []Account{
			{ID: 1, Priority: 10, Schedulable: true},
			{ID: 2, Priority: 20, Schedulable: false},
		},
	}}
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: map[int64]*accountLatencyMonitorGroupRuntime{
		7: {
			accounts: map[int64]*AccountLatencyMonitorAccountState{
				2: {AccountID: 2, LastSuccess: accountLatencyMonitorBoolPtr(true), LastLatencyMs: accountLatencyMonitorInt64Ptr(1_000)},
			},
			backups:      []int64{2},
			activeSince:  map[int64]time.Time{1: time.Now()},
			issues:       make(map[int64]map[string][]time.Time),
			recentIssues: make(map[int64][]time.Time),
		},
	}}

	if _, err := monitor.tryActivateHealthyBackup(context.Background(), DefaultAccountLatencyMonitorGroup(7), 1, "test", "test"); err != nil {
		t.Fatalf("activate healthy backup: %v", err)
	}
	if !repo.listHadDeadline {
		t.Fatal("account switch repository calls did not receive a deadline")
	}
}

func TestAccountLatencyMonitorSetSchedulableAccountsRejectsAccountOutsideGroup(t *testing.T) {
	repo := &accountLatencyMonitorRepoStub{accounts: []Account{
		{ID: 1, Schedulable: true},
		{ID: 2, Schedulable: false},
	}}
	monitor := &AccountLatencyMonitor{accountRepo: repo, runtime: make(map[int64]*accountLatencyMonitorGroupRuntime)}

	err := monitor.setSchedulableAccounts(context.Background(), AccountLatencyMonitorGroup{GroupID: 7}, []int64{99}, "test", "test")
	if err == nil {
		t.Fatal("expected account outside group to be rejected")
	}
	if len(repo.setCalls) != 0 {
		t.Fatalf("invalid target changed scheduling: %#v", repo.setCalls)
	}
}

func accountIDsString(ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	out := make([]byte, 0, len(ids)*3)
	for i, id := range ids {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, byte('0'+id))
	}
	return string(out)
}
