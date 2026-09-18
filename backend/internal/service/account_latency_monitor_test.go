package service

import "testing"

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

func TestNormalizeAccountLatencyMonitorGroup_AppliesDefaultsWithoutCappingBackupCount(t *testing.T) {
	cfg := AccountLatencyMonitorGroup{GroupID: 1, BackupCount: 3, AlwaysEnabledIDs: []int64{2, 2, -1, 4}}
	normalizeAccountLatencyMonitorGroup(&cfg)

	if cfg.LatencyThresholdSec != 10 || cfg.FailureWindowSec != 30 || cfg.ConsecutiveFailures != 2 {
		t.Fatalf("unexpected request defaults: %#v", cfg)
	}
	if cfg.ProbeIntervalSec != 60 || cfg.ProbeTimeoutSec != 30 || cfg.ProbeConcurrency != 4 {
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
