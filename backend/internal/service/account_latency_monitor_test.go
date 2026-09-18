package service

import (
	"testing"
	"time"
)

func TestSortAccountLatencyMonitorStates_UsesThreeLayers(t *testing.T) {
	fastSlow := int64(9_000)
	fastQuick := int64(1_000)
	slow := int64(11_000)
	states := []AccountLatencyMonitorAccountState{
		{AccountID: 1, AccountPriority: 0, LastLatencyMs: &slow},
		{AccountID: 2, AccountPriority: 5, LastLatencyMs: &fastQuick},
		{AccountID: 3, AccountPriority: 1, LastLatencyMs: &fastSlow},
		{AccountID: 4, AccountPriority: 1},
	}

	sortAccountLatencyMonitorStates(states, 10_000)
	got := accountIDsString([]int64{states[0].AccountID, states[1].AccountID, states[2].AccountID, states[3].AccountID})
	if want := "3,2,1,4"; got != want {
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
	currentID, backups := accountLatencyMonitorTargets(candidates, nil, 2)
	if currentID != 2 {
		t.Fatalf("current account = %d, want 2", currentID)
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
	currentID, backups := accountLatencyMonitorTargets([]int64{1, 2, 3, 4}, []int64{2}, 2)
	if currentID != 1 {
		t.Fatalf("current account = %d, want 1", currentID)
	}
	if got, want := accountIDsString(backups), "3,4"; got != want {
		t.Fatalf("backup accounts = %s, want %s", got, want)
	}
}

func TestAccountLatencyMonitorTargets_DoesNotUseAlwaysEnabledAccountAsCurrent(t *testing.T) {
	currentID, backups := accountLatencyMonitorTargets([]int64{1, 2, 3, 4}, []int64{1}, 2)
	if currentID != 2 {
		t.Fatalf("current account = %d, want 2", currentID)
	}
	if got, want := accountIDsString(backups), "3,4"; got != want {
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
	if cfg.ProbeIntervalSec != 60 || cfg.IdleProbeIntervalSec != 1800 || cfg.ProbeTimeoutSec != 30 || cfg.ProbeConcurrency != 4 || cfg.SwitchCooldownSec != 600 || cfg.ImmediateThresholdSec != 40 {
		t.Fatalf("unexpected probe defaults: %#v", cfg)
	}
	if cfg.BackupCount != 3 {
		t.Fatalf("backup_count = %d, want 3", cfg.BackupCount)
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
