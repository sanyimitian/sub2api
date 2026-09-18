package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	accountLatencyMonitorDefaultModel              = "gpt-5.6-sol"
	accountLatencyMonitorDefaultPrompt             = "hi"
	accountLatencyMonitorSettingsRefresh           = 5 * time.Second
	accountLatencyMonitorDefaultThreshold          = 10
	accountLatencyMonitorDefaultWindow             = 30
	accountLatencyMonitorDefaultFailures           = 2
	accountLatencyMonitorDefaultProbePeriod        = 60
	accountLatencyMonitorDefaultIdlePeriod         = 30 * 60
	accountLatencyMonitorDefaultBackupCount        = 2
	accountLatencyMonitorDefaultTimeout            = 30
	accountLatencyMonitorDefaultConcurrency        = 4
	accountLatencyMonitorDefaultSwitchCooldown     = 10 * 60
	accountLatencyMonitorDefaultImmediateThreshold = 40
)

// AccountLatencyMonitorSettings is persisted as one JSON system setting so the
// monitor can be deployed independently of account and group schema changes.
type AccountLatencyMonitorSettings struct {
	Groups []AccountLatencyMonitorGroup `json:"groups"`
}

func emptyAccountLatencyMonitorSettings() AccountLatencyMonitorSettings {
	return AccountLatencyMonitorSettings{Groups: make([]AccountLatencyMonitorGroup, 0)}
}

// AccountLatencyMonitorGroup defines the health policy of one account group.
// Every numeric field is persisted so defaults can be adjusted per group.
type AccountLatencyMonitorGroup struct {
	GroupID               int64   `json:"group_id"`
	Enabled               bool    `json:"enabled"`
	LatencyThresholdSec   int     `json:"latency_threshold_seconds"`
	FailureWindowSec      int     `json:"failure_window_seconds"`
	ConsecutiveFailures   int     `json:"consecutive_failures"`
	ProbeIntervalSec      int     `json:"probe_interval_seconds"`
	IdleProbeIntervalSec  int     `json:"idle_probe_interval_seconds"`
	ProbeTimeoutSec       int     `json:"probe_timeout_seconds"`
	ProbeConcurrency      int     `json:"probe_concurrency"`
	BackupCount           int     `json:"backup_count"`
	AlwaysEnabledIDs      []int64 `json:"always_enabled_account_ids"`
	ProbeModel            string  `json:"probe_model"`
	ProbePrompt           string  `json:"probe_prompt"`
	ProbeReasoning        string  `json:"probe_reasoning_effort"`
	SwitchCooldownSec     int     `json:"switch_cooldown_seconds"`
	ImmediateThresholdSec int     `json:"immediate_switch_threshold_seconds"`
}

type AccountLatencyMonitorAccountState struct {
	AccountID          int64      `json:"account_id"`
	AccountPriority    int        `json:"account_priority"`
	ConsecutiveFailure int        `json:"consecutive_failures"`
	LastLatencyMs      *int64     `json:"last_latency_ms,omitempty"`
	LastSuccess        *bool      `json:"last_success,omitempty"`
	LastObservedAt     *time.Time `json:"last_observed_at,omitempty"`
}

type AccountLatencyMonitorGroupState struct {
	GroupID          int64                               `json:"group_id"`
	ActiveAccountIDs []int64                             `json:"active_account_ids"`
	BackupAccountIDs []int64                             `json:"backup_account_ids"`
	LastProbeAt      *time.Time                          `json:"last_probe_at,omitempty"`
	LastSwitchAt     *time.Time                          `json:"last_switch_at,omitempty"`
	Accounts         []AccountLatencyMonitorAccountState `json:"accounts"`
}

type accountLatencyMonitorGroupRuntime struct {
	accounts        map[int64]*AccountLatencyMonitorAccountState
	backups         []int64
	lastProbe       time.Time
	lastSwitch      time.Time
	lastUserRequest time.Time
	probing         bool
	switching       bool
	issues          map[int64]map[string][]time.Time
}

const (
	accountLatencyMonitorIssueFailure = "failure"
	accountLatencyMonitorIssueLatency = "latency"
)

// AccountLatencyMonitor records user-request first-token outcomes and keeps a
// tested standby pool for each configured group.
type AccountLatencyMonitor struct {
	settingRepo SettingRepository
	accountRepo AccountRepository
	groupRepo   GroupRepository
	tester      *AccountTestService

	mu      sync.Mutex
	runtime map[int64]*accountLatencyMonitorGroupRuntime
	stop    chan struct{}
	once    sync.Once

	settingsMu       sync.RWMutex
	cachedSettings   AccountLatencyMonitorSettings
	runtimePersistMu sync.Mutex
}

func NewAccountLatencyMonitor(settingRepo SettingRepository, accountRepo AccountRepository, groupRepo GroupRepository, tester *AccountTestService) *AccountLatencyMonitor {
	m := &AccountLatencyMonitor{
		settingRepo: settingRepo,
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		tester:      tester,
		runtime:     make(map[int64]*accountLatencyMonitorGroupRuntime),
		stop:        make(chan struct{}),
	}
	go m.run()
	return m
}

func DefaultAccountLatencyMonitorGroup(groupID int64) AccountLatencyMonitorGroup {
	return AccountLatencyMonitorGroup{
		GroupID:               groupID,
		Enabled:               true,
		LatencyThresholdSec:   accountLatencyMonitorDefaultThreshold,
		FailureWindowSec:      accountLatencyMonitorDefaultWindow,
		ConsecutiveFailures:   accountLatencyMonitorDefaultFailures,
		ProbeIntervalSec:      accountLatencyMonitorDefaultProbePeriod,
		IdleProbeIntervalSec:  accountLatencyMonitorDefaultIdlePeriod,
		ProbeTimeoutSec:       accountLatencyMonitorDefaultTimeout,
		ProbeConcurrency:      accountLatencyMonitorDefaultConcurrency,
		BackupCount:           accountLatencyMonitorDefaultBackupCount,
		ProbeModel:            accountLatencyMonitorDefaultModel,
		ProbePrompt:           accountLatencyMonitorDefaultPrompt,
		ProbeReasoning:        "low",
		SwitchCooldownSec:     accountLatencyMonitorDefaultSwitchCooldown,
		ImmediateThresholdSec: accountLatencyMonitorDefaultImmediateThreshold,
	}
}

func (m *AccountLatencyMonitor) GetSettings(ctx context.Context) (*AccountLatencyMonitorSettings, error) {
	settings, err := m.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	m.cacheSettings(settings)
	return &settings, nil
}

func (m *AccountLatencyMonitor) loadSettings(ctx context.Context) (AccountLatencyMonitorSettings, error) {
	if m == nil || m.settingRepo == nil {
		return emptyAccountLatencyMonitorSettings(), nil
	}
	raw, err := m.settingRepo.GetValue(ctx, SettingKeyAccountLatencyMonitorSettings)
	if errors.Is(err, ErrSettingNotFound) || raw == "" {
		return emptyAccountLatencyMonitorSettings(), nil
	}
	if err != nil {
		return AccountLatencyMonitorSettings{}, fmt.Errorf("get account latency monitor settings: %w", err)
	}
	var settings AccountLatencyMonitorSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return AccountLatencyMonitorSettings{}, fmt.Errorf("decode account latency monitor settings: %w", err)
	}
	for i := range settings.Groups {
		normalizeAccountLatencyMonitorGroup(&settings.Groups[i])
	}
	return cloneAccountLatencyMonitorSettings(settings), nil
}

func (m *AccountLatencyMonitor) UpdateSettings(ctx context.Context, settings *AccountLatencyMonitorSettings) error {
	if m == nil || m.settingRepo == nil {
		return errors.New("account latency monitor is unavailable")
	}
	if settings == nil {
		return errors.New("settings cannot be nil")
	}

	normalized := cloneAccountLatencyMonitorSettings(*settings)
	seen := make(map[int64]struct{}, len(normalized.Groups))
	for i := range normalized.Groups {
		cfg := &normalized.Groups[i]
		normalizeAccountLatencyMonitorGroup(cfg)
		if cfg.GroupID <= 0 {
			return errors.New("group_id must be positive")
		}
		if _, ok := seen[cfg.GroupID]; ok {
			return errors.New("group_id cannot be duplicated")
		}
		seen[cfg.GroupID] = struct{}{}
		if err := m.validateGroupConfiguration(ctx, *cfg); err != nil {
			return err
		}
	}

	payload, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("encode account latency monitor settings: %w", err)
	}
	if err := m.settingRepo.Set(ctx, SettingKeyAccountLatencyMonitorSettings, string(payload)); err != nil {
		return err
	}

	*settings = cloneAccountLatencyMonitorSettings(normalized)
	m.cacheSettings(normalized)
	m.resetRuntimeForSettings(normalized)
	return nil
}

func (m *AccountLatencyMonitor) validateGroupConfiguration(ctx context.Context, cfg AccountLatencyMonitorGroup) error {
	if m.groupRepo == nil || m.accountRepo == nil {
		return errors.New("account latency monitor dependencies are unavailable")
	}
	if _, err := m.groupRepo.GetByID(ctx, cfg.GroupID); err != nil {
		return fmt.Errorf("get group %d: %w", cfg.GroupID, err)
	}
	if len(cfg.AlwaysEnabledIDs) == 0 {
		return nil
	}
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return fmt.Errorf("list accounts in group %d: %w", cfg.GroupID, err)
	}
	members := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		members[account.ID] = struct{}{}
	}
	for _, accountID := range cfg.AlwaysEnabledIDs {
		if _, ok := members[accountID]; !ok {
			return fmt.Errorf("always_enabled_account_id %d does not belong to group %d", accountID, cfg.GroupID)
		}
	}
	return nil
}

func normalizeAccountLatencyMonitorGroup(g *AccountLatencyMonitorGroup) {
	if g.LatencyThresholdSec <= 0 {
		g.LatencyThresholdSec = accountLatencyMonitorDefaultThreshold
	}
	if g.FailureWindowSec <= 0 {
		g.FailureWindowSec = accountLatencyMonitorDefaultWindow
	}
	if g.ConsecutiveFailures <= 0 {
		g.ConsecutiveFailures = accountLatencyMonitorDefaultFailures
	}
	if g.ProbeIntervalSec <= 0 {
		g.ProbeIntervalSec = accountLatencyMonitorDefaultProbePeriod
	}
	if g.IdleProbeIntervalSec <= 0 {
		g.IdleProbeIntervalSec = accountLatencyMonitorDefaultIdlePeriod
	}
	if g.ProbeTimeoutSec <= 0 {
		g.ProbeTimeoutSec = accountLatencyMonitorDefaultTimeout
	}
	if g.ProbeConcurrency <= 0 {
		g.ProbeConcurrency = accountLatencyMonitorDefaultConcurrency
	}
	if g.BackupCount <= 0 {
		g.BackupCount = accountLatencyMonitorDefaultBackupCount
	}
	if g.SwitchCooldownSec <= 0 {
		g.SwitchCooldownSec = accountLatencyMonitorDefaultSwitchCooldown
	}
	if g.ImmediateThresholdSec <= 0 {
		g.ImmediateThresholdSec = accountLatencyMonitorDefaultImmediateThreshold
	}
	if strings.TrimSpace(g.ProbeModel) == "" {
		g.ProbeModel = accountLatencyMonitorDefaultModel
	} else {
		g.ProbeModel = strings.TrimSpace(g.ProbeModel)
	}
	if strings.TrimSpace(g.ProbePrompt) == "" {
		g.ProbePrompt = accountLatencyMonitorDefaultPrompt
	} else {
		g.ProbePrompt = strings.TrimSpace(g.ProbePrompt)
	}
	if strings.TrimSpace(g.ProbeReasoning) == "" {
		g.ProbeReasoning = "low"
	} else {
		g.ProbeReasoning = strings.TrimSpace(g.ProbeReasoning)
	}
	seen := make(map[int64]struct{}, len(g.AlwaysEnabledIDs))
	ids := make([]int64, 0, len(g.AlwaysEnabledIDs))
	for _, id := range g.AlwaysEnabledIDs {
		if id > 0 {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	g.AlwaysEnabledIDs = ids
}

func cloneAccountLatencyMonitorSettings(settings AccountLatencyMonitorSettings) AccountLatencyMonitorSettings {
	out := AccountLatencyMonitorSettings{Groups: make([]AccountLatencyMonitorGroup, len(settings.Groups))}
	copy(out.Groups, settings.Groups)
	for i := range out.Groups {
		out.Groups[i].AlwaysEnabledIDs = append([]int64(nil), settings.Groups[i].AlwaysEnabledIDs...)
	}
	return out
}

func (m *AccountLatencyMonitor) cacheSettings(settings AccountLatencyMonitorSettings) {
	m.settingsMu.Lock()
	m.cachedSettings = cloneAccountLatencyMonitorSettings(settings)
	m.settingsMu.Unlock()
}

func (m *AccountLatencyMonitor) cachedSettingsSnapshot() AccountLatencyMonitorSettings {
	m.settingsMu.RLock()
	settings := cloneAccountLatencyMonitorSettings(m.cachedSettings)
	m.settingsMu.RUnlock()
	return settings
}

func (m *AccountLatencyMonitor) cachedGroup(groupID int64) (AccountLatencyMonitorGroup, bool) {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	for _, cfg := range m.cachedSettings.Groups {
		if cfg.GroupID == groupID && cfg.Enabled {
			cfg.AlwaysEnabledIDs = append([]int64(nil), cfg.AlwaysEnabledIDs...)
			return cfg, true
		}
	}
	return AccountLatencyMonitorGroup{}, false
}

func (m *AccountLatencyMonitor) resetRuntimeForSettings(settings AccountLatencyMonitorSettings) {
	configured := make(map[int64]struct{}, len(settings.Groups))
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cfg := range settings.Groups {
		configured[cfg.GroupID] = struct{}{}
		rt := m.ensureRuntimeLocked(cfg.GroupID)
		rt.backups = nil
		rt.lastProbe = time.Time{}
	}
	for groupID := range m.runtime {
		if _, ok := configured[groupID]; !ok {
			delete(m.runtime, groupID)
		}
	}
}

// RecordRequest reports one user request. Repeated same-kind anomalies within
// the configured window trigger an immediate switch to a previously tested backup.
func (m *AccountLatencyMonitor) RecordRequest(_ context.Context, groupID, accountID int64, firstTokenMs *int, success bool) {
	if m == nil || groupID <= 0 || accountID <= 0 {
		return
	}
	cfg, enabled := m.cachedGroup(groupID)
	if !enabled {
		return
	}

	issue := ""
	if !success || firstTokenMs == nil {
		issue = accountLatencyMonitorIssueFailure
	} else if *firstTokenMs > cfg.LatencyThresholdSec*1000 {
		issue = accountLatencyMonitorIssueLatency
	}

	now := time.Now().UTC()
	m.mu.Lock()
	rt := m.ensureRuntimeLocked(groupID)
	state := m.ensureAccountStateLocked(rt, accountID)
	rt.lastUserRequest = now
	state.LastSuccess = accountLatencyMonitorBoolPtr(success)
	state.LastObservedAt = accountLatencyMonitorTimePtr(now)
	if firstTokenMs == nil {
		state.LastLatencyMs = nil
	} else {
		state.LastLatencyMs = accountLatencyMonitorInt64Ptr(int64(*firstTokenMs))
	}

	issueCount, windowIssueCount := accountLatencyMonitorWindowIssueCount(rt, accountID, issue, now, time.Duration(cfg.FailureWindowSec)*time.Second)
	state.ConsecutiveFailure = windowIssueCount

	shouldFailover := false
	immediateSwitch := firstTokenMs != nil && *firstTokenMs > cfg.ImmediateThresholdSec*1000
	if issue != "" && (immediateSwitch || issueCount >= cfg.ConsecutiveFailures) && !rt.switching {
		rt.switching = true
		shouldFailover = true
	}
	m.mu.Unlock()

	if shouldFailover {
		go m.failover(context.Background(), cfg, accountID)
	}
}

func accountLatencyMonitorWindowIssueCount(rt *accountLatencyMonitorGroupRuntime, accountID int64, issue string, now time.Time, window time.Duration) (int, int) {
	byKind := rt.issues[accountID]
	if byKind == nil {
		byKind = make(map[string][]time.Time)
		rt.issues[accountID] = byKind
	}
	cutoff := now.Add(-window)
	maxCount := 0
	for kind, timestamps := range byKind {
		kept := timestamps[:0]
		for _, timestamp := range timestamps {
			if !timestamp.Before(cutoff) {
				kept = append(kept, timestamp)
			}
		}
		if len(kept) == 0 {
			delete(byKind, kind)
			continue
		}
		byKind[kind] = kept
		if len(kept) > maxCount {
			maxCount = len(kept)
		}
	}
	if issue == "" {
		if len(byKind) == 0 {
			delete(rt.issues, accountID)
		}
		return 0, maxCount
	}
	byKind[issue] = append(byKind[issue], now)
	issueCount := len(byKind[issue])
	if issueCount > maxCount {
		maxCount = issueCount
	}
	return issueCount, maxCount
}

func (m *AccountLatencyMonitor) GetRuntime(ctx context.Context) ([]AccountLatencyMonitorGroupState, error) {
	settings, err := m.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	states := make([]AccountLatencyMonitorGroupState, 0, len(settings.Groups))
	for _, cfg := range settings.Groups {
		state := m.snapshotRuntime(cfg.GroupID)
		accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
		if err != nil {
			return nil, fmt.Errorf("list accounts in group %d: %w", cfg.GroupID, err)
		}
		for _, account := range accounts {
			if account.Schedulable {
				state.ActiveAccountIDs = append(state.ActiveAccountIDs, account.ID)
			}
		}
		// Runtime state may outlive a manual scheduling change. Do not expose an
		// active or permanently enabled account as a standby while the next probe
		// refreshes the pool.
		state.BackupAccountIDs = accountLatencyMonitorStandbyIDs(
			state.BackupAccountIDs,
			state.ActiveAccountIDs,
			cfg.AlwaysEnabledIDs,
		)
		priorities := make(map[int64]int, len(accounts))
		for _, account := range accounts {
			priorities[account.ID] = accountLatencyMonitorPriority(account, cfg.GroupID)
		}
		for i := range state.Accounts {
			state.Accounts[i].AccountPriority = priorities[state.Accounts[i].AccountID]
		}
		sortAccountLatencyMonitorStates(state.Accounts, int64(cfg.LatencyThresholdSec*1000))
		sort.Slice(state.ActiveAccountIDs, func(i, j int) bool { return state.ActiveAccountIDs[i] < state.ActiveAccountIDs[j] })
		states = append(states, state)
	}
	return states, nil
}

func (m *AccountLatencyMonitor) snapshotRuntime(groupID int64) AccountLatencyMonitorGroupState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := AccountLatencyMonitorGroupState{GroupID: groupID}
	rt := m.runtime[groupID]
	if rt == nil {
		return state
	}
	state.BackupAccountIDs = append(state.BackupAccountIDs, rt.backups...)
	if !rt.lastProbe.IsZero() {
		state.LastProbeAt = accountLatencyMonitorTimePtr(rt.lastProbe)
	}
	if !rt.lastSwitch.IsZero() {
		state.LastSwitchAt = accountLatencyMonitorTimePtr(rt.lastSwitch)
	}
	for _, account := range rt.accounts {
		state.Accounts = append(state.Accounts, cloneAccountLatencyMonitorState(*account))
	}
	sort.Slice(state.Accounts, func(i, j int) bool { return state.Accounts[i].AccountID < state.Accounts[j].AccountID })
	return state
}

func cloneAccountLatencyMonitorState(state AccountLatencyMonitorAccountState) AccountLatencyMonitorAccountState {
	if state.LastLatencyMs != nil {
		state.LastLatencyMs = accountLatencyMonitorInt64Ptr(*state.LastLatencyMs)
	}
	if state.LastSuccess != nil {
		state.LastSuccess = accountLatencyMonitorBoolPtr(*state.LastSuccess)
	}
	if state.LastObservedAt != nil {
		state.LastObservedAt = accountLatencyMonitorTimePtr(*state.LastObservedAt)
	}
	return state
}

func sortAccountLatencyMonitorStates(states []AccountLatencyMonitorAccountState, thresholdMs int64) {
	sort.Slice(states, func(i, j int) bool {
		iFast := states[i].LastLatencyMs != nil && *states[i].LastLatencyMs < thresholdMs
		jFast := states[j].LastLatencyMs != nil && *states[j].LastLatencyMs < thresholdMs
		if iFast != jFast {
			return iFast
		}
		if states[i].AccountPriority != states[j].AccountPriority {
			return states[i].AccountPriority < states[j].AccountPriority
		}
		if states[i].LastLatencyMs == nil || states[j].LastLatencyMs == nil {
			if states[i].LastLatencyMs == nil && states[j].LastLatencyMs != nil {
				return false
			}
			if states[i].LastLatencyMs != nil && states[j].LastLatencyMs == nil {
				return true
			}
			return states[i].AccountID < states[j].AccountID
		}
		if *states[i].LastLatencyMs != *states[j].LastLatencyMs {
			return *states[i].LastLatencyMs < *states[j].LastLatencyMs
		}
		return states[i].AccountID < states[j].AccountID
	})
}

func (m *AccountLatencyMonitor) run() {
	m.loadRuntimeSwitches(context.Background())
	m.refreshCachedSettings(context.Background())
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastSettingsRefresh := time.Now()
	for {
		select {
		case <-ticker.C:
			if time.Since(lastSettingsRefresh) >= accountLatencyMonitorSettingsRefresh {
				m.refreshCachedSettings(context.Background())
				lastSettingsRefresh = time.Now()
			}
			m.checkDueGroups()
		case <-m.stop:
			return
		}
	}
}

func (m *AccountLatencyMonitor) refreshCachedSettings(ctx context.Context) {
	settings, err := m.loadSettings(ctx)
	if err == nil {
		m.cacheSettings(settings)
	}
}

func (m *AccountLatencyMonitor) Stop() {
	if m != nil {
		m.once.Do(func() { close(m.stop) })
	}
}

func (m *AccountLatencyMonitor) checkDueGroups() {
	for _, cfg := range m.cachedSettingsSnapshot().Groups {
		if !cfg.Enabled || !m.startDueProbe(cfg) {
			continue
		}
		if m.hasBackups(cfg.GroupID) {
			go m.probeBackups(context.Background(), cfg)
		} else {
			go m.probeAllAndFinish(context.Background(), cfg, nil, m.needsInitialActivation(cfg.GroupID), true)
		}
	}
}

func (m *AccountLatencyMonitor) startDueProbe(cfg AccountLatencyMonitorGroup) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.ensureRuntimeLocked(cfg.GroupID)
	due := rt.lastProbe.IsZero() || time.Since(rt.lastProbe) >= accountLatencyMonitorProbeInterval(cfg, rt)
	if !due || rt.probing {
		return false
	}
	rt.probing = true
	return true
}

func accountLatencyMonitorProbeInterval(cfg AccountLatencyMonitorGroup, rt *accountLatencyMonitorGroupRuntime) time.Duration {
	if !rt.lastUserRequest.IsZero() && rt.lastUserRequest.After(rt.lastProbe) {
		return time.Duration(cfg.ProbeIntervalSec) * time.Second
	}
	return time.Duration(cfg.IdleProbeIntervalSec) * time.Second
}

func (m *AccountLatencyMonitor) hasBackups(groupID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.ensureRuntimeLocked(groupID).backups) > 0
}

func (m *AccountLatencyMonitor) needsInitialActivation(groupID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureRuntimeLocked(groupID).lastProbe.IsZero()
}

func (m *AccountLatencyMonitor) finishProbe(groupID int64) {
	m.mu.Lock()
	rt := m.ensureRuntimeLocked(groupID)
	rt.probing = false
	rt.lastProbe = time.Now().UTC()
	m.mu.Unlock()
}

func (m *AccountLatencyMonitor) failover(ctx context.Context, cfg AccountLatencyMonitorGroup, failedID int64) {
	defer func() {
		m.mu.Lock()
		m.ensureRuntimeLocked(cfg.GroupID).switching = false
		m.mu.Unlock()
	}()

	if backupID, ok := m.healthyBackup(cfg, failedID); ok {
		if err := m.setSchedulableAccounts(ctx, cfg, []int64{backupID}); err == nil {
			// Keep the selected backup as the current account. The probe only
			// refreshes the standby pool after a request-triggered switch.
			m.probeAll(ctx, cfg, map[int64]struct{}{failedID: {}}, false)
		}
		m.markProbed(cfg.GroupID)
		return
	}
	// Only when the current backup pool cannot provide a healthy alternative do
	// we retest every group account and choose a replacement pool.
	m.probeAll(ctx, cfg, map[int64]struct{}{failedID: {}}, true)
	m.markProbed(cfg.GroupID)
}

func (m *AccountLatencyMonitor) healthyBackup(cfg AccountLatencyMonitorGroup, failedID int64) (int64, bool) {
	thresholdMs := int64(cfg.LatencyThresholdSec * 1000)
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.ensureRuntimeLocked(cfg.GroupID)
	for _, accountID := range rt.backups {
		if accountID == failedID {
			continue
		}
		state := rt.accounts[accountID]
		if state == nil || state.LastSuccess == nil || !*state.LastSuccess || state.LastLatencyMs == nil || *state.LastLatencyMs >= thresholdMs {
			continue
		}
		return accountID, true
	}
	return 0, false
}

func (m *AccountLatencyMonitor) probeBackups(ctx context.Context, cfg AccountLatencyMonitorGroup) {
	defer m.finishProbe(cfg.GroupID)
	backupIDs := m.backupIDs(cfg.GroupID)
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return
	}
	activeIDs := accountLatencyMonitorSchedulableIDs(accounts)
	currentIDs := accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs)
	byID := make(map[int64]Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	monitorIDs := make(map[int64]struct{}, len(backupIDs)+len(accounts))
	for _, accountID := range backupIDs {
		if _, ok := byID[accountID]; !ok {
			m.probeAllPeriodic(ctx, cfg, nil, len(currentIDs) == 0)
			return
		}
		monitorIDs[accountID] = struct{}{}
	}
	for _, account := range accounts {
		if account.Schedulable {
			monitorIDs[account.ID] = struct{}{}
		}
	}
	if len(monitorIDs) == 0 {
		m.probeAllPeriodic(ctx, cfg, nil, len(currentIDs) == 0)
		return
	}
	monitored := make([]Account, 0, len(monitorIDs))
	for accountID := range monitorIDs {
		monitored = append(monitored, byID[accountID])
	}

	results := m.probeAccounts(ctx, cfg, monitored)
	thresholdMs := int64(cfg.LatencyThresholdSec * 1000)
	currentSet := accountLatencyMonitorExcludedIDs(currentIDs)
	activeUnhealthy := false
	poolUnhealthy := false
	for _, result := range results {
		if !result.success || result.latency >= thresholdMs {
			poolUnhealthy = true
			if _, active := currentSet[result.account.ID]; active {
				activeUnhealthy = true
			}
		}
	}
	if len(currentIDs) > 1 {
		// Older versions and manual changes can leave several dynamic accounts
		// schedulable. Reconcile that state on the next probe while preserving
		// configured always-enabled accounts.
		currentID := accountLatencyMonitorPreferredCurrentID(results, currentIDs, cfg, thresholdMs)
		if currentID != 0 {
			_ = m.setSchedulableAccounts(ctx, cfg, []int64{currentID})
			currentIDs = []int64{currentID}
			currentSet = accountLatencyMonitorExcludedIDs(currentIDs)
			activeUnhealthy = false
			for _, result := range results {
				if result.account.ID == currentID && (!result.success || result.latency >= thresholdMs) {
					activeUnhealthy = true
				}
			}
		}
	}
	if poolUnhealthy {
		// A failed standby must be replaced, but it must not cause a healthy
		// currently scheduled account to be swapped out.
		m.probeAllPeriodic(ctx, cfg, nil, activeUnhealthy)
		return
	}

	// A successful periodic probe still changes the relative latency of the
	// existing standby accounts. Refresh their ordered pool from these latest
	// results without changing the currently scheduled account.
	m.replaceBackupsFromResults(cfg, results, activeIDs)
}

func (m *AccountLatencyMonitor) probeAllAndFinish(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate, periodic bool) {
	defer m.finishProbe(cfg.GroupID)
	m.probeAllWithPolicy(ctx, cfg, excluded, activate, periodic)
}

func (m *AccountLatencyMonitor) probeAll(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate bool) {
	m.probeAllWithPolicy(ctx, cfg, excluded, activate, false)
}

func (m *AccountLatencyMonitor) probeAllPeriodic(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate bool) {
	m.probeAllWithPolicy(ctx, cfg, excluded, activate, true)
}

func (m *AccountLatencyMonitor) probeAllWithPolicy(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate, periodic bool) {
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return
	}
	results := m.probeAccounts(ctx, cfg, accounts)
	if !activate {
		m.replaceBackupsFromResults(cfg, results, accountLatencyMonitorSchedulableIDs(accounts))
		return
	}
	if periodic && !accountLatencyMonitorPeriodicSwitchAllowed(m.runtimeLastSwitch(cfg.GroupID), accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs), cfg.SwitchCooldownSec, time.Now().UTC()) {
		m.replaceBackupsFromResults(cfg, results, accountLatencyMonitorSchedulableIDs(accounts))
		return
	}
	// Keep every successful result through ranking. Long-term enabled accounts
	// can appear before the dynamic current account and must not consume its
	// slot or one of the configured standby slots.
	candidates := selectAccountLatencyMonitorBackups(results, cfg.GroupID, int64(cfg.LatencyThresholdSec*1000), len(accounts), excluded)
	currentID, backups := accountLatencyMonitorTargets(candidates, cfg.AlwaysEnabledIDs, cfg.BackupCount)
	m.mu.Lock()
	m.ensureRuntimeLocked(cfg.GroupID).backups = append([]int64(nil), backups...)
	m.mu.Unlock()
	if activate {
		active := make([]int64, 0, 1)
		if currentID != 0 {
			active = append(active, currentID)
		}
		_ = m.setSchedulableAccounts(ctx, cfg, active)
	}
}

type accountLatencyProbeResult struct {
	account Account
	latency int64
	success bool
}

func (m *AccountLatencyMonitor) probeAccounts(ctx context.Context, cfg AccountLatencyMonitorGroup, accounts []Account) []accountLatencyProbeResult {
	if m.tester == nil || len(accounts) == 0 {
		return nil
	}
	results := make([]accountLatencyProbeResult, 0, len(accounts))
	resultCh := make(chan accountLatencyProbeResult, len(accounts))
	sem := make(chan struct{}, cfg.ProbeConcurrency)
	var wg sync.WaitGroup
	for _, account := range accounts {
		account := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			probeCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.ProbeTimeoutSec)*time.Second)
			result, err := m.tester.RunLatencyMonitorProbe(probeCtx, account.ID, cfg.ProbeModel, cfg.ProbePrompt, cfg.ProbeReasoning)
			cancel()
			probe := accountLatencyProbeResult{account: account}
			if err == nil && result != nil {
				probe.latency = result.LatencyMs
				probe.success = result.Status == "success"
			}
			var latency *int64
			if probe.success {
				latency = accountLatencyMonitorInt64Ptr(probe.latency)
			}
			m.observeProbe(cfg.GroupID, account.ID, latency, probe.success)
			resultCh <- probe
		}()
	}
	wg.Wait()
	close(resultCh)
	for result := range resultCh {
		results = append(results, result)
	}
	return results
}

func (m *AccountLatencyMonitor) observeProbe(groupID, accountID int64, latency *int64, success bool) {
	now := time.Now().UTC()
	m.mu.Lock()
	state := m.ensureAccountStateLocked(m.ensureRuntimeLocked(groupID), accountID)
	state.LastLatencyMs = latency
	state.LastSuccess = accountLatencyMonitorBoolPtr(success)
	state.LastObservedAt = accountLatencyMonitorTimePtr(now)
	m.mu.Unlock()
}

func selectAccountLatencyMonitorBackups(results []accountLatencyProbeResult, groupID, thresholdMs int64, backupCount int, excluded map[int64]struct{}) []int64 {
	if backupCount <= 0 {
		return nil
	}
	fast := make([]accountLatencyProbeResult, 0, len(results))
	slow := make([]accountLatencyProbeResult, 0, len(results))
	for _, result := range results {
		if !result.success {
			continue
		}
		if _, omit := excluded[result.account.ID]; omit {
			continue
		}
		if result.latency < thresholdMs {
			fast = append(fast, result)
			continue
		}
		slow = append(slow, result)
	}

	sortResults := func(candidates []accountLatencyProbeResult) {
		sort.Slice(candidates, func(i, j int) bool {
			iPriority := accountLatencyMonitorPriority(candidates[i].account, groupID)
			jPriority := accountLatencyMonitorPriority(candidates[j].account, groupID)
			if iPriority != jPriority {
				return iPriority < jPriority
			}
			if candidates[i].latency != candidates[j].latency {
				return candidates[i].latency < candidates[j].latency
			}
			return candidates[i].account.ID < candidates[j].account.ID
		})
	}
	sortResults(fast)
	sortResults(slow)

	chosen := make([]int64, 0, min(backupCount, len(fast)+len(slow)))
	for _, candidates := range [][]accountLatencyProbeResult{fast, slow} {
		for _, candidate := range candidates {
			if len(chosen) == backupCount {
				return chosen
			}
			chosen = append(chosen, candidate.account.ID)
		}
	}
	return chosen
}

// accountLatencyMonitorTargets separates the primary scheduling account from
// true standby accounts. Always-enabled accounts remain schedulable but do not
// consume a standby slot.
func accountLatencyMonitorTargets(candidates, alwaysEnabled []int64, backupCount int) (int64, []int64) {
	active := make(map[int64]struct{}, len(alwaysEnabled)+1)
	for _, accountID := range alwaysEnabled {
		active[accountID] = struct{}{}
	}
	currentID := int64(0)
	for _, accountID := range candidates {
		if _, isAlwaysEnabled := active[accountID]; !isAlwaysEnabled {
			currentID = accountID
			break
		}
	}
	if currentID == 0 {
		return 0, nil
	}
	active[currentID] = struct{}{}
	backups := make([]int64, 0, min(backupCount, len(candidates)-1))
	for _, accountID := range candidates[1:] {
		if _, isActive := active[accountID]; isActive {
			continue
		}
		backups = append(backups, accountID)
		if len(backups) == backupCount {
			break
		}
	}
	return currentID, backups
}

func (m *AccountLatencyMonitor) replaceBackupsFromResults(cfg AccountLatencyMonitorGroup, results []accountLatencyProbeResult, activeIDs []int64) {
	excluded := accountLatencyMonitorExcludedIDs(activeIDs, cfg.AlwaysEnabledIDs)
	backups := selectAccountLatencyMonitorBackups(
		results,
		cfg.GroupID,
		int64(cfg.LatencyThresholdSec*1000),
		cfg.BackupCount,
		excluded,
	)
	m.mu.Lock()
	m.ensureRuntimeLocked(cfg.GroupID).backups = backups
	m.mu.Unlock()
}

func accountLatencyMonitorExcludedIDs(ids ...[]int64) map[int64]struct{} {
	excluded := make(map[int64]struct{})
	for _, values := range ids {
		for _, id := range values {
			if id > 0 {
				excluded[id] = struct{}{}
			}
		}
	}
	return excluded
}

func accountLatencyMonitorStandbyIDs(backups, activeIDs, alwaysEnabled []int64) []int64 {
	excluded := accountLatencyMonitorExcludedIDs(activeIDs, alwaysEnabled)
	filtered := make([]int64, 0, len(backups))
	seen := make(map[int64]struct{}, len(backups))
	for _, accountID := range backups {
		if _, skip := excluded[accountID]; skip || accountID <= 0 {
			continue
		}
		if _, duplicate := seen[accountID]; duplicate {
			continue
		}
		seen[accountID] = struct{}{}
		filtered = append(filtered, accountID)
	}
	return filtered
}

func accountLatencyMonitorSchedulableIDs(accounts []Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account.Schedulable {
			ids = append(ids, account.ID)
		}
	}
	return ids
}

func accountLatencyMonitorCurrentIDs(accounts []Account, alwaysEnabled []int64) []int64 {
	alwaysEnabledSet := accountLatencyMonitorExcludedIDs(alwaysEnabled)
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if !account.Schedulable {
			continue
		}
		if _, isAlwaysEnabled := alwaysEnabledSet[account.ID]; !isAlwaysEnabled {
			ids = append(ids, account.ID)
		}
	}
	return ids
}

func accountLatencyMonitorPreferredCurrentID(results []accountLatencyProbeResult, currentIDs []int64, cfg AccountLatencyMonitorGroup, thresholdMs int64) int64 {
	currentSet := accountLatencyMonitorExcludedIDs(currentIDs)
	filtered := make([]accountLatencyProbeResult, 0, len(currentIDs))
	for _, result := range results {
		if _, ok := currentSet[result.account.ID]; ok {
			filtered = append(filtered, result)
		}
	}
	if candidates := selectAccountLatencyMonitorBackups(filtered, cfg.GroupID, thresholdMs, len(filtered), nil); len(candidates) > 0 {
		return candidates[0]
	}
	if len(currentIDs) > 0 {
		return currentIDs[0]
	}
	return 0
}

func accountLatencyMonitorPeriodicSwitchAllowed(lastSwitch time.Time, currentIDs []int64, cooldownSec int, now time.Time) bool {
	if len(currentIDs) == 0 || lastSwitch.IsZero() {
		return true
	}
	return now.Sub(lastSwitch) >= time.Duration(cooldownSec)*time.Second
}

func accountLatencyMonitorPriority(account Account, _ int64) int {
	return account.Priority
}

func (m *AccountLatencyMonitor) backupIDs(groupID int64) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int64(nil), m.ensureRuntimeLocked(groupID).backups...)
}

func (m *AccountLatencyMonitor) runtimeLastSwitch(groupID int64) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureRuntimeLocked(groupID).lastSwitch
}

func (m *AccountLatencyMonitor) markProbed(groupID int64) {
	m.mu.Lock()
	m.ensureRuntimeLocked(groupID).lastProbe = time.Now().UTC()
	m.mu.Unlock()
}

// setSchedulableAccounts changes only the scheduling switch. Account status is
// owned by account management and must not be changed by latency monitoring.
func (m *AccountLatencyMonitor) setSchedulableAccounts(ctx context.Context, cfg AccountLatencyMonitorGroup, active []int64) error {
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return err
	}
	previousCurrent := accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs)
	allowed := make(map[int64]bool, len(active)+len(cfg.AlwaysEnabledIDs))
	for _, accountID := range active {
		allowed[accountID] = true
	}
	for _, accountID := range cfg.AlwaysEnabledIDs {
		allowed[accountID] = true
	}
	for i := range accounts {
		account := accounts[i]
		wantSchedulable := allowed[account.ID]
		accounts[i].Schedulable = wantSchedulable
		if account.Schedulable != wantSchedulable {
			if err := m.accountRepo.SetSchedulable(ctx, account.ID, wantSchedulable); err != nil {
				return err
			}
		}
	}
	if !accountLatencyMonitorSameAccountIDs(previousCurrent, accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs)) {
		switchAt := time.Now().UTC()
		m.mu.Lock()
		m.ensureRuntimeLocked(cfg.GroupID).lastSwitch = switchAt
		m.mu.Unlock()
		m.persistRuntimeSwitch(ctx, cfg.GroupID, switchAt)
	}
	return nil
}

type accountLatencyMonitorRuntimeState struct {
	LastSwitch map[string]time.Time `json:"last_switch"`
}

func (m *AccountLatencyMonitor) loadRuntimeSwitches(ctx context.Context) {
	if m == nil || m.settingRepo == nil {
		return
	}
	raw, err := m.settingRepo.GetValue(ctx, SettingKeyAccountLatencyMonitorRuntime)
	if err != nil || raw == "" {
		return
	}
	var state accountLatencyMonitorRuntimeState
	if json.Unmarshal([]byte(raw), &state) != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for groupID, value := range state.LastSwitch {
		parsed, err := strconv.ParseInt(groupID, 10, 64)
		if err == nil && parsed > 0 {
			m.ensureRuntimeLocked(parsed).lastSwitch = value
		}
	}
}

func (m *AccountLatencyMonitor) persistRuntimeSwitch(ctx context.Context, groupID int64, switchAt time.Time) {
	if m == nil || m.settingRepo == nil || groupID <= 0 {
		return
	}
	m.runtimePersistMu.Lock()
	defer m.runtimePersistMu.Unlock()
	state := accountLatencyMonitorRuntimeState{LastSwitch: make(map[string]time.Time)}
	if raw, err := m.settingRepo.GetValue(ctx, SettingKeyAccountLatencyMonitorRuntime); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &state)
		if state.LastSwitch == nil {
			state.LastSwitch = make(map[string]time.Time)
		}
	}
	state.LastSwitch[strconv.FormatInt(groupID, 10)] = switchAt
	payload, err := json.Marshal(state)
	if err == nil {
		_ = m.settingRepo.Set(ctx, SettingKeyAccountLatencyMonitorRuntime, string(payload))
	}
}

func accountLatencyMonitorSameAccountIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	leftSet := accountLatencyMonitorExcludedIDs(left)
	for _, id := range right {
		if _, ok := leftSet[id]; !ok {
			return false
		}
	}
	return true
}

func (m *AccountLatencyMonitor) listGroupAccounts(ctx context.Context, groupID int64) ([]Account, error) {
	if m == nil || m.accountRepo == nil {
		return nil, errors.New("account repository is unavailable")
	}
	return m.accountRepo.ListAllWithFilters(ctx, "", "", "", "", groupID, "")
}

func (m *AccountLatencyMonitor) ensureRuntimeLocked(groupID int64) *accountLatencyMonitorGroupRuntime {
	rt := m.runtime[groupID]
	if rt == nil {
		rt = &accountLatencyMonitorGroupRuntime{
			accounts: make(map[int64]*AccountLatencyMonitorAccountState),
			issues:   make(map[int64]map[string][]time.Time),
		}
		m.runtime[groupID] = rt
	}
	return rt
}

func (m *AccountLatencyMonitor) ensureAccountStateLocked(rt *accountLatencyMonitorGroupRuntime, accountID int64) *AccountLatencyMonitorAccountState {
	state := rt.accounts[accountID]
	if state == nil {
		state = &AccountLatencyMonitorAccountState{AccountID: accountID}
		rt.accounts[accountID] = state
	}
	return state
}

func accountLatencyMonitorInt64Ptr(value int64) *int64 { return &value }

func accountLatencyMonitorBoolPtr(value bool) *bool { return &value }

func accountLatencyMonitorTimePtr(value time.Time) *time.Time { return &value }
