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
		{AccountID: 1, GroupPriority: 0, LastLatencyMs: &slow},
		{AccountID: 2, GroupPriority: 5, LastLatencyMs: &fastQuick},
		{AccountID: 3, GroupPriority: 1, LastLatencyMs: &fastSlow},
		{AccountID: 4, GroupPriority: 1},
	}

	sortAccountLatencyMonitorStates(states, 10_000)
	got := accountIDsString([]int64{states[0].AccountID, states[1].AccountID, states[2].AccountID, states[3].AccountID})
	if want := "3,2,1,4"; got != want {
		t.Fatalf("sorted account IDs = %s, want %s", got, want)
	}
}

func TestSelectAccountLatencyMonitorBackups_PrefersFastThenGroupPriority(t *testing.T) {
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 0}}}, latency: 12_000, success: true},
		{account: Account{ID: 2, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 5}}}, latency: 8_000, success: true},
		{account: Account{ID: 3, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 1}}}, latency: 9_000, success: true},
		{account: Account{ID: 4, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 1}}}, latency: 7_000, success: false},
	}

	chosen := selectAccountLatencyMonitorBackups(results, 7, 10_000, 3, nil)
	if got, want := accountIDsString(chosen), "3,2,1"; got != want {
		t.Fatalf("selected backups = %s, want %s", got, want)
	}
}

func TestSelectAccountLatencyMonitorBackups_PrefersGroupPriorityWithinFastAccounts(t *testing.T) {
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 4}}}, latency: 1_000, success: true},
		{account: Account{ID: 2, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 1}}}, latency: 9_000, success: true},
		{account: Account{ID: 3, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 1}}}, latency: 7_000, success: true},
	}

	chosen := selectAccountLatencyMonitorBackups(results, 7, 10_000, 3, nil)
	if got, want := accountIDsString(chosen), "3,2,1"; got != want {
		t.Fatalf("selected backups = %s, want %s", got, want)
	}
}

func TestSelectAccountLatencyMonitorBackups_SortsEqualPriorityByLatency(t *testing.T) {
	results := []accountLatencyProbeResult{
		{account: Account{ID: 1, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 2}}}, latency: 8_000, success: true},
		{account: Account{ID: 2, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 2}}}, latency: 6_000, success: true},
		{account: Account{ID: 3, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 2}}}, latency: 7_000, success: true},
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

func TestNormalizeAccountLatencyMonitorGroup_AppliesDefaultsWithoutCappingBackupCount(t *testing.T) {
	cfg := AccountLatencyMonitorGroup{GroupID: 1, BackupCount: 3, AlwaysEnabledIDs: []int64{2, 2, -1, 4}}
	normalizeAccountLatencyMonitorGroup(&cfg)

	if cfg.LatencyThresholdSec != 10 || cfg.FailureWindowSec != 30 || cfg.ConsecutiveFailures != 2 {
		t.Fatalf("unexpected request defaults: %#v", cfg)
	}
	if cfg.ProbeIntervalSec != 60 || cfg.IdleProbeIntervalSec != 1800 || cfg.ProbeTimeoutSec != 30 || cfg.ProbeConcurrency != 4 {
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
