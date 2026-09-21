package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	accountLatencyMonitorDefaultActiveAccountCount = 1
	accountLatencyMonitorDefaultTimeout            = 30
	accountLatencyMonitorDefaultConcurrency        = 4
	accountLatencyMonitorDefaultSwitchCooldown     = 10 * 60
	accountLatencyMonitorDefaultImmediateThreshold = 40
	accountLatencyMonitorDefaultRecentIssueWindow  = 10 * 60
	accountLatencyMonitorSwitchOperationTimeout    = 10 * time.Second
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
	ActiveAccountCount    int     `json:"active_account_count"`
	BackupCount           int     `json:"backup_count"`
	AlwaysEnabledIDs      []int64 `json:"always_enabled_account_ids"`
	ProbeModel            string  `json:"probe_model"`
	ProbePrompt           string  `json:"probe_prompt"`
	ProbeReasoning        string  `json:"probe_reasoning_effort"`
	SwitchCooldownSec     int     `json:"switch_cooldown_seconds"`
	ImmediateThresholdSec int     `json:"immediate_switch_threshold_seconds"`
	RecentIssueWindowSec  int     `json:"recent_issue_window_seconds"`
}

type AccountLatencyMonitorAccountState struct {
	AccountID          int64      `json:"account_id"`
	AccountPriority    int        `json:"account_priority"`
	ConsecutiveFailure int        `json:"consecutive_failures"`
	RecentIssueCount   int        `json:"recent_issue_count"`
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
	SwitchHistory    []AccountLatencyMonitorSwitchRecord `json:"switch_history"`
	Accounts         []AccountLatencyMonitorAccountState `json:"accounts"`
}

type AccountLatencyMonitorSwitchRecord struct {
	SwitchedAt         time.Time `json:"switched_at"`
	PreviousAccountIDs []int64   `json:"previous_account_ids"`
	CurrentAccountIDs  []int64   `json:"current_account_ids"`
	ReasonCode         string    `json:"reason_code"`
	Reason             string    `json:"reason"`
}

type accountLatencyMonitorGroupRuntime struct {
	accounts        map[int64]*AccountLatencyMonitorAccountState
	backups         []int64
	switchHistory   []AccountLatencyMonitorSwitchRecord
	activeSince     map[int64]time.Time
	lastProbe       time.Time
	lastSwitch      time.Time
	lastUserRequest time.Time
	probing         bool
	switching       bool
	switchDone      chan struct{}
	issues          map[int64]map[string][]time.Time
	recentIssues    map[int64][]time.Time
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
	watches map[int64]map[int64]map[*AccountLatencyRequestWatch]struct{}
	stop    chan struct{}
	once    sync.Once

	settingsMu         sync.RWMutex
	cachedSettings     AccountLatencyMonitorSettings
	settingsLoaded     bool
	runtimePersistGate chan struct{}
}

func NewAccountLatencyMonitor(settingRepo SettingRepository, accountRepo AccountRepository, groupRepo GroupRepository, tester *AccountTestService) *AccountLatencyMonitor {
	m := &AccountLatencyMonitor{
		settingRepo: settingRepo,
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		tester:      tester,
		runtime:     make(map[int64]*accountLatencyMonitorGroupRuntime),
		watches:     make(map[int64]map[int64]map[*AccountLatencyRequestWatch]struct{}),
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
		ActiveAccountCount:    accountLatencyMonitorDefaultActiveAccountCount,
		BackupCount:           accountLatencyMonitorDefaultBackupCount,
		ProbeModel:            accountLatencyMonitorDefaultModel,
		ProbePrompt:           accountLatencyMonitorDefaultPrompt,
		ProbeReasoning:        "low",
		SwitchCooldownSec:     accountLatencyMonitorDefaultSwitchCooldown,
		ImmediateThresholdSec: accountLatencyMonitorDefaultImmediateThreshold,
		RecentIssueWindowSec:  accountLatencyMonitorDefaultRecentIssueWindow,
	}
}

func (m *AccountLatencyMonitor) GetSettings(ctx context.Context) (*AccountLatencyMonitorSettings, error) {
	settings, err := m.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	changed := m.cachedSettingsChanged(settings)
	m.cacheSettings(settings)
	m.reconcileRuntimeForSettings(settings, changed)
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
	if g.ActiveAccountCount <= 0 {
		g.ActiveAccountCount = accountLatencyMonitorDefaultActiveAccountCount
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
	if g.RecentIssueWindowSec <= 0 {
		g.RecentIssueWindowSec = accountLatencyMonitorDefaultRecentIssueWindow
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
	m.settingsLoaded = true
	m.settingsMu.Unlock()
}

func (m *AccountLatencyMonitor) cachedSettingsChanged(settings AccountLatencyMonitorSettings) bool {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.settingsLoaded && !reflect.DeepEqual(m.cachedSettings, settings)
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
	m.reconcileRuntimeForSettings(settings, true)
}

func (m *AccountLatencyMonitor) reconcileRuntimeForSettings(settings AccountLatencyMonitorSettings, resetEnabled bool) {
	enabled := make(map[int64]struct{}, len(settings.Groups))
	watches := make([]*AccountLatencyRequestWatch, 0)
	m.mu.Lock()
	for _, cfg := range settings.Groups {
		if !cfg.Enabled {
			continue
		}
		enabled[cfg.GroupID] = struct{}{}
		if resetEnabled {
			rt := m.ensureRuntimeLocked(cfg.GroupID)
			rt.backups = nil
			rt.lastProbe = time.Time{}
		}
	}
	for groupID := range m.runtime {
		if _, ok := enabled[groupID]; !ok {
			delete(m.runtime, groupID)
		}
	}
	for groupID, byAccount := range m.watches {
		if _, ok := enabled[groupID]; ok && !resetEnabled {
			continue
		}
		for _, requests := range byAccount {
			for watch := range requests {
				watches = append(watches, watch)
			}
		}
		delete(m.watches, groupID)
	}
	m.mu.Unlock()

	for _, watch := range watches {
		watch.disarm()
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
	state.RecentIssueCount = accountLatencyMonitorRecentIssueCount(rt, accountID, now, time.Duration(cfg.RecentIssueWindowSec)*time.Second, issue != "")

	shouldFailover := false
	immediateSwitch := firstTokenMs != nil && *firstTokenMs > cfg.ImmediateThresholdSec*1000
	reasonCode, reason := accountLatencyMonitorRequestReason(cfg, accountID, firstTokenMs, issue, issueCount, immediateSwitch)
	if issue != "" && (immediateSwitch || issueCount >= cfg.ConsecutiveFailures) && !rt.switching {
		rt.switching = true
		rt.switchDone = make(chan struct{})
		shouldFailover = true
	}
	m.mu.Unlock()

	if shouldFailover {
		go m.failover(context.Background(), cfg, accountID, reasonCode, reason)
	}
}

func accountLatencyMonitorRequestReason(cfg AccountLatencyMonitorGroup, accountID int64, firstTokenMs *int, issue string, issueCount int, immediate bool) (string, string) {
	if immediate {
		return "user_single_latency", fmt.Sprintf("账号 %d 单次用户请求首字延迟超过 %d 秒", accountID, cfg.ImmediateThresholdSec)
	}
	if issue == accountLatencyMonitorIssueLatency {
		return "user_latency_window", fmt.Sprintf("账号 %d 在 %d 秒内有 %d 次用户请求首字延迟超过 %d 秒", accountID, cfg.FailureWindowSec, issueCount, cfg.LatencyThresholdSec)
	}
	return "user_failure_window", fmt.Sprintf("账号 %d 在 %d 秒内有 %d 次用户请求发生同类失败或未返回首字", accountID, cfg.FailureWindowSec, issueCount)
}

func (m *AccountLatencyMonitor) recordFirstTokenDeadlineAndSwitch(ctx context.Context, cfg AccountLatencyMonitorGroup, accountID int64, observedLatency time.Duration, immediate, addIssue bool) (int64, map[int64]struct{}) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	m.mu.Lock()
	rt := m.ensureRuntimeLocked(cfg.GroupID)
	state := m.ensureAccountStateLocked(rt, accountID)
	rt.lastUserRequest = now
	state.LastSuccess = accountLatencyMonitorBoolPtr(false)
	state.LastObservedAt = accountLatencyMonitorTimePtr(now)
	latencyMs := observedLatency.Milliseconds()
	if latencyMs < 0 {
		latencyMs = 0
	}
	state.LastLatencyMs = accountLatencyMonitorInt64Ptr(latencyMs)
	issue := ""
	if addIssue {
		issue = accountLatencyMonitorIssueLatency
	}
	issueCount, windowIssueCount := accountLatencyMonitorWindowIssueCount(
		rt,
		accountID,
		issue,
		now,
		time.Duration(cfg.FailureWindowSec)*time.Second,
	)
	state.ConsecutiveFailure = windowIssueCount
	state.RecentIssueCount = accountLatencyMonitorRecentIssueCount(rt, accountID, now, time.Duration(cfg.RecentIssueWindowSec)*time.Second, addIssue)
	shouldSwitch := immediate || issueCount >= cfg.ConsecutiveFailures
	if !shouldSwitch {
		m.mu.Unlock()
		return 0, nil
	}
	for rt.switching {
		done := rt.switchDone
		m.mu.Unlock()
		if done == nil {
			return 0, nil
		}
		select {
		case <-done:
		case <-ctx.Done():
			return 0, nil
		}
		m.mu.Lock()
		rt = m.runtime[cfg.GroupID]
		if rt == nil {
			m.mu.Unlock()
			return 0, nil
		}
	}
	if ctx.Err() != nil {
		m.mu.Unlock()
		return 0, nil
	}
	rt.switching = true
	rt.switchDone = make(chan struct{})
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		if rt := m.runtime[cfg.GroupID]; rt != nil {
			rt.switching = false
			if rt.switchDone != nil {
				close(rt.switchDone)
				rt.switchDone = nil
			}
		}
		m.mu.Unlock()
	}()

	reasonCode := "user_latency_window"
	reason := fmt.Sprintf("账号 %d 在 %d 秒内有 %d 次用户请求未在 %d 秒内返回首字", accountID, cfg.FailureWindowSec, issueCount, cfg.LatencyThresholdSec)
	if immediate {
		reasonCode = "user_single_latency"
		reason = fmt.Sprintf("账号 %d 单次用户请求在 %d 秒内未返回首字", accountID, cfg.ImmediateThresholdSec)
	}
	switchResult, err := m.tryActivateHealthyBackup(ctx, cfg, accountID, reasonCode, reason)
	if err != nil || !switchResult.stillCurrent {
		return 0, nil
	}
	m.mu.Lock()
	rt = m.runtime[cfg.GroupID]
	if rt == nil {
		m.mu.Unlock()
		return 0, nil
	}
	if switchResult.switched {
		rt.backups = accountLatencyMonitorStandbyIDs(rt.backups, switchResult.nextActive, cfg.AlwaysEnabledIDs)
	}
	m.mu.Unlock()
	if !switchResult.backupAvailable {
		go m.probeAll(context.Background(), cfg, map[int64]struct{}{accountID: {}}, false, "request_timeout_rebuild", "首字超时后补充备用池")
		return 0, nil
	}
	if !switchResult.switched {
		return 0, nil
	}

	m.switchPendingRequestWatches(cfg.GroupID, accountID, switchResult.backupID, switchResult.excludedIDs)
	go func() {
		m.probeAll(context.Background(), cfg, map[int64]struct{}{accountID: {}}, false, "request_failover", "用户请求触发切换后重新补充备用池")
		m.markProbed(cfg.GroupID)
	}()
	return switchResult.backupID, switchResult.excludedIDs
}

func (m *AccountLatencyMonitor) completeDeadlineObservedRequest(cfg AccountLatencyMonitorGroup, accountID int64, firstTokenMs *int, success bool) {
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.ensureRuntimeLocked(cfg.GroupID)
	state := m.ensureAccountStateLocked(rt, accountID)
	state.LastSuccess = accountLatencyMonitorBoolPtr(success)
	state.LastObservedAt = accountLatencyMonitorTimePtr(now)
	if firstTokenMs != nil {
		state.LastLatencyMs = accountLatencyMonitorInt64Ptr(int64(*firstTokenMs))
	}
	_, state.ConsecutiveFailure = accountLatencyMonitorWindowIssueCount(rt, accountID, "", now, time.Duration(cfg.FailureWindowSec)*time.Second)
	state.RecentIssueCount = accountLatencyMonitorRecentIssueCount(rt, accountID, now, time.Duration(cfg.RecentIssueWindowSec)*time.Second, false)
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

func accountLatencyMonitorRecentIssueCount(rt *accountLatencyMonitorGroupRuntime, accountID int64, now time.Time, window time.Duration, add bool) int {
	if rt.recentIssues == nil {
		rt.recentIssues = make(map[int64][]time.Time)
	}
	cutoff := now.Add(-window)
	timestamps := rt.recentIssues[accountID]
	kept := timestamps[:0]
	for _, timestamp := range timestamps {
		if !timestamp.Before(cutoff) {
			kept = append(kept, timestamp)
		}
	}
	if add {
		kept = append(kept, now)
	}
	if len(kept) == 0 {
		delete(rt.recentIssues, accountID)
		return 0
	}
	rt.recentIssues[accountID] = kept
	return len(kept)
}

func (m *AccountLatencyMonitor) GetRuntime(ctx context.Context) ([]AccountLatencyMonitorGroupState, error) {
	settings, err := m.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	states := make([]AccountLatencyMonitorGroupState, 0, len(settings.Groups))
	for _, cfg := range settings.Groups {
		accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
		if err != nil {
			return nil, fmt.Errorf("list accounts in group %d: %w", cfg.GroupID, err)
		}
		state := m.snapshotRuntime(cfg, accounts)
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

func (m *AccountLatencyMonitor) snapshotRuntime(cfg AccountLatencyMonitorGroup, accounts []Account) AccountLatencyMonitorGroupState {
	validAccountIDs := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		validAccountIDs[account.ID] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	state := AccountLatencyMonitorGroupState{GroupID: cfg.GroupID}
	rt := m.runtime[cfg.GroupID]
	if rt == nil {
		return state
	}
	pruneAccountLatencyMonitorRuntime(rt, validAccountIDs)
	state.BackupAccountIDs = append(state.BackupAccountIDs, rt.backups...)
	if !rt.lastProbe.IsZero() {
		state.LastProbeAt = accountLatencyMonitorTimePtr(rt.lastProbe)
	}
	if !rt.lastSwitch.IsZero() {
		state.LastSwitchAt = accountLatencyMonitorTimePtr(rt.lastSwitch)
	}
	state.SwitchHistory = cloneAccountLatencyMonitorSwitchHistory(rt.switchHistory)
	now := time.Now().UTC()
	for accountID, account := range rt.accounts {
		account.RecentIssueCount = accountLatencyMonitorRecentIssueCount(rt, accountID, now, time.Duration(cfg.RecentIssueWindowSec)*time.Second, false)
		state.Accounts = append(state.Accounts, cloneAccountLatencyMonitorState(*account))
	}
	sort.Slice(state.Accounts, func(i, j int) bool { return state.Accounts[i].AccountID < state.Accounts[j].AccountID })
	return state
}

func pruneAccountLatencyMonitorRuntime(rt *accountLatencyMonitorGroupRuntime, validAccountIDs map[int64]struct{}) {
	if rt == nil {
		return
	}
	for accountID := range rt.accounts {
		if _, valid := validAccountIDs[accountID]; !valid {
			delete(rt.accounts, accountID)
		}
	}
	for accountID := range rt.issues {
		if _, valid := validAccountIDs[accountID]; !valid {
			delete(rt.issues, accountID)
		}
	}
	for accountID := range rt.recentIssues {
		if _, valid := validAccountIDs[accountID]; !valid {
			delete(rt.recentIssues, accountID)
		}
	}
	for accountID := range rt.activeSince {
		if _, valid := validAccountIDs[accountID]; !valid {
			delete(rt.activeSince, accountID)
		}
	}
	backups := make([]int64, 0, len(rt.backups))
	for _, accountID := range rt.backups {
		if _, valid := validAccountIDs[accountID]; valid {
			backups = append(backups, accountID)
		}
	}
	rt.backups = backups
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
		if states[i].RecentIssueCount != states[j].RecentIssueCount {
			return states[i].RecentIssueCount < states[j].RecentIssueCount
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
		changed := m.cachedSettingsChanged(settings)
		m.cacheSettings(settings)
		m.reconcileRuntimeForSettings(settings, changed)
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
			go m.probeAllAndFinish(context.Background(), cfg, nil, m.needsInitialActivation(cfg.GroupID), true, "monitor_rebalance", "监控初始化或重建备用池")
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

func (m *AccountLatencyMonitor) failover(ctx context.Context, cfg AccountLatencyMonitorGroup, failedID int64, reasonCode, reason string) {
	defer func() {
		m.mu.Lock()
		if rt := m.runtime[cfg.GroupID]; rt != nil {
			rt.switching = false
			if rt.switchDone != nil {
				close(rt.switchDone)
				rt.switchDone = nil
			}
		}
		m.mu.Unlock()
	}()

	switchResult, err := m.tryActivateHealthyBackup(ctx, cfg, failedID, reasonCode, reason)
	if err != nil {
		if switchResult.backupAvailable {
			m.markProbed(cfg.GroupID)
		}
		return
	}
	if !switchResult.stillCurrent {
		// An in-flight request can finish after a prior failover has already
		// removed its account from scheduling. It must not replace the new current account.
		return
	}
	m.mu.Lock()
	runtimeExists := m.runtime[cfg.GroupID] != nil
	m.mu.Unlock()
	if !runtimeExists {
		return
	}

	if switchResult.switched {
		m.switchPendingRequestWatches(cfg.GroupID, failedID, switchResult.backupID, switchResult.excludedIDs)
		// Keep the selected backup as the current account. The probe only
		// refreshes the standby pool after a request-triggered switch.
		m.probeAll(ctx, cfg, map[int64]struct{}{failedID: {}}, false, "request_failover", "用户请求触发切换后重新补充备用池")
		m.markProbed(cfg.GroupID)
		return
	}
	// Only when the current backup pool cannot provide a healthy alternative do
	// we retest every group account and choose a replacement pool.
	m.probeAll(ctx, cfg, map[int64]struct{}{failedID: {}}, true, reasonCode, reason)
	m.markProbed(cfg.GroupID)
}

type accountLatencyBackupSwitchResult struct {
	backupID        int64
	nextActive      []int64
	excludedIDs     map[int64]struct{}
	stillCurrent    bool
	backupAvailable bool
	switched        bool
}

func (m *AccountLatencyMonitor) tryActivateHealthyBackup(
	ctx context.Context,
	cfg AccountLatencyMonitorGroup,
	failedID int64,
	reasonCode string,
	reason string,
) (accountLatencyBackupSwitchResult, error) {
	var result accountLatencyBackupSwitchResult
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, accountLatencyMonitorSwitchOperationTimeout)
	defer cancel()
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return result, err
	}
	currentIDs := accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs)
	if _, result.stillCurrent = accountLatencyMonitorExcludedIDs(currentIDs)[failedID]; !result.stillCurrent {
		return result, nil
	}

	backupID, ok := m.healthyBackup(cfg, failedID, accounts)
	result.backupAvailable = ok
	if !ok {
		return result, nil
	}
	result.backupID = backupID
	result.nextActive = accountLatencyMonitorReplaceFailedCurrent(currentIDs, failedID, backupID, cfg.ActiveAccountCount)
	if err := m.setSchedulableAccounts(ctx, cfg, result.nextActive, reasonCode, reason); err != nil {
		return result, err
	}

	result.excludedIDs = make(map[int64]struct{}, len(accounts)-1)
	for _, account := range accounts {
		if account.ID != backupID {
			result.excludedIDs[account.ID] = struct{}{}
		}
	}
	result.switched = true
	return result, nil
}

func (m *AccountLatencyMonitor) healthyBackup(cfg AccountLatencyMonitorGroup, failedID int64, accounts []Account) (int64, bool) {
	thresholdMs := int64(cfg.LatencyThresholdSec * 1000)
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.runtime[cfg.GroupID]
	if rt == nil {
		return 0, false
	}
	priorities := make(map[int64]int, len(accounts))
	members := make(map[int64]struct{}, len(accounts))
	unavailable := accountLatencyMonitorExcludedIDs(cfg.AlwaysEnabledIDs)
	for _, account := range accounts {
		members[account.ID] = struct{}{}
		priorities[account.ID] = accountLatencyMonitorPriority(account, cfg.GroupID)
		if account.Schedulable {
			unavailable[account.ID] = struct{}{}
		}
	}
	var bestID int64
	var best *AccountLatencyMonitorAccountState
	validBackups := make([]int64, 0, len(rt.backups))
	now := time.Now().UTC()
	for _, accountID := range rt.backups {
		if _, exists := members[accountID]; !exists {
			continue
		}
		validBackups = append(validBackups, accountID)
		if accountID == failedID {
			continue
		}
		if _, skip := unavailable[accountID]; skip {
			continue
		}
		state := rt.accounts[accountID]
		if state == nil || state.LastSuccess == nil || !*state.LastSuccess || state.LastLatencyMs == nil || *state.LastLatencyMs >= thresholdMs {
			continue
		}
		state.RecentIssueCount = accountLatencyMonitorRecentIssueCount(rt, accountID, now, time.Duration(cfg.RecentIssueWindowSec)*time.Second, false)
		if best == nil || state.RecentIssueCount < best.RecentIssueCount ||
			(state.RecentIssueCount == best.RecentIssueCount && priorities[accountID] < priorities[bestID]) ||
			(state.RecentIssueCount == best.RecentIssueCount && priorities[accountID] == priorities[bestID] && *state.LastLatencyMs < *best.LastLatencyMs) ||
			(state.RecentIssueCount == best.RecentIssueCount && priorities[accountID] == priorities[bestID] && *state.LastLatencyMs == *best.LastLatencyMs && accountID < bestID) {
			bestID, best = accountID, state
		}
	}
	rt.backups = validBackups
	return bestID, best != nil
}

func (m *AccountLatencyMonitor) probeBackups(ctx context.Context, cfg AccountLatencyMonitorGroup) {
	defer m.finishProbe(cfg.GroupID)
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return
	}
	activeIDs := accountLatencyMonitorSchedulableIDs(accounts)
	currentIDs := accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs)
	m.syncActiveSince(cfg.GroupID, currentIDs, time.Now().UTC())
	backupIDs := m.backupIDsExcludingActive(cfg.GroupID, activeIDs)
	byID := make(map[int64]Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	// Periodic probes only validate the standby pool. User traffic is the
	// health signal for currently scheduled accounts.
	monitorIDs := make(map[int64]struct{}, len(backupIDs))
	for _, accountID := range backupIDs {
		if _, ok := byID[accountID]; !ok {
			m.probeAllPeriodic(ctx, cfg, nil, false)
			return
		}
		monitorIDs[accountID] = struct{}{}
	}
	if len(monitorIDs) == 0 {
		m.probeAllPeriodic(ctx, cfg, nil, false)
		return
	}
	monitored := make([]Account, 0, len(monitorIDs))
	for accountID := range monitorIDs {
		monitored = append(monitored, byID[accountID])
	}

	results := m.probeAccounts(ctx, cfg, monitored)
	thresholdMs := int64(cfg.LatencyThresholdSec * 1000)
	if candidate, ok := accountLatencyMonitorBestPriorityBackup(results, currentIDs, accounts, thresholdMs); ok &&
		m.activeAccountSwitchAllowed(cfg.GroupID, accountLatencyMonitorWorstPriorityCurrentID(currentIDs, accounts), cfg.SwitchCooldownSec, time.Now().UTC()) {
		nextActive := replaceWorstPriorityAccount(currentIDs, candidate.account.ID, accounts)
		if !accountLatencyMonitorSameAccountIDs(currentIDs, nextActive) {
			reason := fmt.Sprintf("备用账号 %d 的开启调度优先级高于当前账号，且当前账号已运行达到调度帐号切换周期", candidate.account.ID)
			if err := m.setSchedulableAccounts(ctx, cfg, nextActive, "periodic_priority_upgrade", reason); err == nil {
				// Refill the standby pool only after the new account is active.
				m.probeAll(ctx, cfg, nil, false, "periodic_rebalance", "周期切换后重新补充备用池")
				return
			}
		}
	}
	poolUnhealthy := false
	for _, result := range results {
		if !result.success || result.latency >= thresholdMs {
			poolUnhealthy = true
		}
	}
	if poolUnhealthy {
		// A failed standby must be replaced, but it must not cause a healthy
		// currently scheduled account to be swapped out.
		m.probeAllPeriodic(ctx, cfg, nil, false)
		return
	}

	// A successful periodic probe still changes the relative latency of the
	// existing standby accounts. Refresh their ordered pool from these latest
	// results without changing the currently scheduled account.
	m.replaceBackupsFromResults(cfg, results, activeIDs)
}

func accountLatencyMonitorBestPriorityBackup(results []accountLatencyProbeResult, currentIDs []int64, accounts []Account, thresholdMs int64) (accountLatencyProbeResult, bool) {
	if len(currentIDs) == 0 {
		return accountLatencyProbeResult{}, false
	}
	priorities := make(map[int64]int, len(accounts))
	for _, account := range accounts {
		priorities[account.ID] = account.Priority
	}
	currentPriority := -1
	for _, currentID := range currentIDs {
		if priorities[currentID] > currentPriority {
			currentPriority = priorities[currentID]
		}
	}
	var best accountLatencyProbeResult
	found := false
	for _, result := range results {
		if !result.success || result.latency >= thresholdMs || result.account.Priority >= currentPriority {
			continue
		}
		if !found || result.recentIssueCount < best.recentIssueCount ||
			(result.recentIssueCount == best.recentIssueCount && result.account.Priority < best.account.Priority) ||
			(result.recentIssueCount == best.recentIssueCount && result.account.Priority == best.account.Priority && result.latency < best.latency) {
			best, found = result, true
		}
	}
	return best, found
}

func replaceWorstPriorityAccount(currentIDs []int64, replacement int64, accounts []Account) []int64 {
	if len(currentIDs) == 0 || replacement <= 0 {
		return append([]int64(nil), currentIDs...)
	}
	worstID := accountLatencyMonitorWorstPriorityCurrentID(currentIDs, accounts)
	worst := 0
	for i := range currentIDs {
		if currentIDs[i] == worstID {
			worst = i
			break
		}
	}
	next := append([]int64(nil), currentIDs...)
	next[worst] = replacement
	return next
}

func accountLatencyMonitorWorstPriorityCurrentID(currentIDs []int64, accounts []Account) int64 {
	if len(currentIDs) == 0 {
		return 0
	}
	priorities := make(map[int64]int, len(accounts))
	for _, account := range accounts {
		priorities[account.ID] = account.Priority
	}
	worstID := currentIDs[0]
	for _, accountID := range currentIDs[1:] {
		if priorities[accountID] > priorities[worstID] || (priorities[accountID] == priorities[worstID] && accountID > worstID) {
			worstID = accountID
		}
	}
	return worstID
}

func (m *AccountLatencyMonitor) probeAllAndFinish(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate, periodic bool, reasonCode, reason string) {
	defer m.finishProbe(cfg.GroupID)
	m.probeAllWithPolicy(ctx, cfg, excluded, activate, periodic, reasonCode, reason)
}

func (m *AccountLatencyMonitor) probeAll(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate bool, reasonCode, reason string) {
	m.probeAllWithPolicy(ctx, cfg, excluded, activate, false, reasonCode, reason)
}

func (m *AccountLatencyMonitor) probeAllPeriodic(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate bool) {
	m.probeAllWithPolicy(ctx, cfg, excluded, activate, true, "periodic_rebalance", "周期探测后重新调整账号池")
}

func (m *AccountLatencyMonitor) probeAllWithPolicy(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate, periodic bool, reasonCode, reason string) {
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return
	}
	if !activate {
		activeIDs := accountLatencyMonitorSchedulableIDs(accounts)
		excludedIDs := accountLatencyMonitorExcludedIDs(activeIDs, cfg.AlwaysEnabledIDs)
		for id := range excluded {
			excludedIDs[id] = struct{}{}
		}
		candidates := make([]Account, 0, len(accounts))
		for _, account := range accounts {
			if _, skip := excludedIDs[account.ID]; !skip {
				candidates = append(candidates, account)
			}
		}
		results := m.probeAccounts(ctx, cfg, candidates)
		m.replaceBackupsFromResults(cfg, results, activeIDs)
		return
	}
	results := m.probeAccounts(ctx, cfg, accounts)
	if periodic && !accountLatencyMonitorPeriodicSwitchAllowed(m.runtimeLastSwitch(cfg.GroupID), accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs), cfg.SwitchCooldownSec, time.Now().UTC()) {
		m.replaceBackupsFromResults(cfg, results, accountLatencyMonitorSchedulableIDs(accounts))
		return
	}
	// Keep every successful result through ranking. Long-term enabled accounts
	// can appear before the dynamic current account and must not consume its
	// slot or one of the configured standby slots.
	candidates := selectAccountLatencyMonitorBackups(results, cfg.GroupID, int64(cfg.LatencyThresholdSec*1000), len(accounts), excluded)
	currentIDs, backups := accountLatencyMonitorTargets(candidates, cfg.AlwaysEnabledIDs, cfg.ActiveAccountCount, cfg.BackupCount)
	m.mu.Lock()
	m.ensureRuntimeLocked(cfg.GroupID).backups = append([]int64(nil), backups...)
	m.mu.Unlock()
	if activate {
		_ = m.setSchedulableAccounts(ctx, cfg, currentIDs, reasonCode, reason)
	}
}

type accountLatencyProbeResult struct {
	account          Account
	latency          int64
	success          bool
	recentIssueCount int
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
			m.observeProbe(cfg, account.ID, latency, probe.success)
			probe.recentIssueCount = m.recentIssueCount(cfg, account.ID, time.Now().UTC())
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

func (m *AccountLatencyMonitor) observeProbe(cfg AccountLatencyMonitorGroup, accountID int64, latency *int64, success bool) {
	now := time.Now().UTC()
	m.mu.Lock()
	rt := m.ensureRuntimeLocked(cfg.GroupID)
	state := m.ensureAccountStateLocked(rt, accountID)
	state.LastLatencyMs = latency
	state.LastSuccess = accountLatencyMonitorBoolPtr(success)
	state.LastObservedAt = accountLatencyMonitorTimePtr(now)
	anomalous := !success || latency == nil || *latency >= int64(cfg.LatencyThresholdSec*1000)
	state.RecentIssueCount = accountLatencyMonitorRecentIssueCount(rt, accountID, now, time.Duration(cfg.RecentIssueWindowSec)*time.Second, anomalous)
	m.mu.Unlock()
}

func (m *AccountLatencyMonitor) recentIssueCount(cfg AccountLatencyMonitorGroup, accountID int64, now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.ensureRuntimeLocked(cfg.GroupID)
	count := accountLatencyMonitorRecentIssueCount(rt, accountID, now, time.Duration(cfg.RecentIssueWindowSec)*time.Second, false)
	m.ensureAccountStateLocked(rt, accountID).RecentIssueCount = count
	return count
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
			if candidates[i].recentIssueCount != candidates[j].recentIssueCount {
				return candidates[i].recentIssueCount < candidates[j].recentIssueCount
			}
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

// accountLatencyMonitorTargets separates the configured number of dynamic
// scheduling accounts from true standby accounts. Always-enabled accounts
// remain schedulable but do not consume either quota.
func accountLatencyMonitorTargets(candidates, alwaysEnabled []int64, activeCount, backupCount int) ([]int64, []int64) {
	active := make(map[int64]struct{}, len(alwaysEnabled)+activeCount)
	for _, accountID := range alwaysEnabled {
		active[accountID] = struct{}{}
	}
	currentIDs := make([]int64, 0, activeCount)
	for _, accountID := range candidates {
		if _, alreadyActive := active[accountID]; alreadyActive {
			continue
		}
		active[accountID] = struct{}{}
		currentIDs = append(currentIDs, accountID)
		if len(currentIDs) == activeCount {
			break
		}
	}
	backups := make([]int64, 0, min(backupCount, len(candidates)-len(currentIDs)))
	for _, accountID := range candidates {
		if _, isActive := active[accountID]; isActive {
			continue
		}
		backups = append(backups, accountID)
		if len(backups) == backupCount {
			break
		}
	}
	return currentIDs, backups
}

func accountLatencyMonitorReplaceFailedCurrent(currentIDs []int64, failedID, backupID int64, activeCount int) []int64 {
	next := make([]int64, 0, activeCount)
	seen := make(map[int64]struct{}, activeCount)
	for _, accountID := range currentIDs {
		if accountID <= 0 || accountID == failedID {
			continue
		}
		if _, duplicate := seen[accountID]; duplicate {
			continue
		}
		seen[accountID] = struct{}{}
		next = append(next, accountID)
		if len(next) == activeCount {
			return next
		}
	}
	if backupID > 0 && len(next) < activeCount {
		if _, exists := seen[backupID]; !exists {
			next = append(next, backupID)
		}
	}
	return next
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
	// The period is measured from the last dynamic account switch, i.e. from
	// when the currently running account was enabled, not from probe time.
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

func (m *AccountLatencyMonitor) backupIDsExcludingActive(groupID int64, activeIDs []int64) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.ensureRuntimeLocked(groupID)
	rt.backups = accountLatencyMonitorStandbyIDs(rt.backups, activeIDs, nil)
	return append([]int64(nil), rt.backups...)
}

func (m *AccountLatencyMonitor) syncActiveSince(groupID int64, currentIDs []int64, now time.Time) {
	current := accountLatencyMonitorExcludedIDs(currentIDs)
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.ensureRuntimeLocked(groupID)
	if rt.activeSince == nil {
		rt.activeSince = make(map[int64]time.Time)
	}
	for accountID := range rt.activeSince {
		if _, active := current[accountID]; !active {
			delete(rt.activeSince, accountID)
		}
	}
	for accountID := range current {
		if _, known := rt.activeSince[accountID]; !known {
			rt.activeSince[accountID] = now
		}
	}
}

func (m *AccountLatencyMonitor) activeAccountSwitchAllowed(groupID, accountID int64, periodSec int, now time.Time) bool {
	if accountID <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.ensureRuntimeLocked(groupID)
	if rt.activeSince == nil {
		rt.activeSince = make(map[int64]time.Time)
	}
	startedAt, ok := rt.activeSince[accountID]
	if !ok {
		rt.activeSince[accountID] = now
		return false
	}
	return now.Sub(startedAt) >= time.Duration(periodSec)*time.Second
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
func (m *AccountLatencyMonitor) setSchedulableAccounts(ctx context.Context, cfg AccountLatencyMonitorGroup, active []int64, reasonCode, reason string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return err
	}
	previousCurrent := accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs)
	members := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		members[account.ID] = struct{}{}
	}
	for _, accountID := range active {
		if _, exists := members[accountID]; !exists {
			return fmt.Errorf("cannot schedule account %d outside group %d", accountID, cfg.GroupID)
		}
	}
	allowed := make(map[int64]bool, len(active)+len(cfg.AlwaysEnabledIDs))
	for _, accountID := range active {
		allowed[accountID] = true
	}
	for _, accountID := range cfg.AlwaysEnabledIDs {
		allowed[accountID] = true
	}
	disabled := make([]int64, 0, len(accounts))
	enabled := make([]int64, 0, len(accounts))
	rollback := func(cause error) error {
		restoreErrors := []error{cause}
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountLatencyMonitorSwitchOperationTimeout)
		defer cancel()
		for i := len(enabled) - 1; i >= 0; i-- {
			if err := m.accountRepo.SetSchedulable(rollbackCtx, enabled[i], false); err != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("rollback disable account %d: %w", enabled[i], err))
			}
		}
		for i := len(disabled) - 1; i >= 0; i-- {
			if err := m.accountRepo.SetSchedulable(rollbackCtx, disabled[i], true); err != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("rollback enable account %d: %w", disabled[i], err))
			}
		}
		return errors.Join(restoreErrors...)
	}
	// Complete the disable phase before enabling any replacement account.
	for i := range accounts {
		account := accounts[i]
		if account.Schedulable && !allowed[account.ID] {
			if err := m.accountRepo.SetSchedulable(ctx, account.ID, false); err != nil {
				return rollback(err)
			}
			accounts[i].Schedulable = false
			disabled = append(disabled, account.ID)
		}
	}
	for i := range accounts {
		account := accounts[i]
		if !account.Schedulable && allowed[account.ID] {
			if err := m.accountRepo.SetSchedulable(ctx, account.ID, true); err != nil {
				return rollback(err)
			}
			accounts[i].Schedulable = true
			enabled = append(enabled, account.ID)
		}
	}
	nextCurrent := accountLatencyMonitorCurrentIDs(accounts, cfg.AlwaysEnabledIDs)
	if !accountLatencyMonitorSameAccountIDs(active, nextCurrent) {
		return rollback(fmt.Errorf("scheduled accounts do not match requested accounts for group %d", cfg.GroupID))
	}
	if !accountLatencyMonitorSameAccountIDs(previousCurrent, nextCurrent) {
		switchAt := time.Now().UTC()
		previousSet := accountLatencyMonitorExcludedIDs(previousCurrent)
		nextSet := accountLatencyMonitorExcludedIDs(nextCurrent)
		if reasonCode == "" {
			reasonCode = "monitor_rebalance"
		}
		if reason == "" {
			reason = "监控重新调整了开启调度的账号"
		}
		record := AccountLatencyMonitorSwitchRecord{
			SwitchedAt:         switchAt,
			PreviousAccountIDs: append([]int64(nil), previousCurrent...),
			CurrentAccountIDs:  append([]int64(nil), nextCurrent...),
			ReasonCode:         reasonCode,
			Reason:             reason,
		}
		m.mu.Lock()
		rt := m.ensureRuntimeLocked(cfg.GroupID)
		rt.lastSwitch = switchAt
		if rt.activeSince == nil {
			rt.activeSince = make(map[int64]time.Time)
		}
		for accountID := range rt.activeSince {
			if _, active := nextSet[accountID]; !active {
				delete(rt.activeSince, accountID)
			}
		}
		for _, accountID := range nextCurrent {
			if _, wasCurrent := previousSet[accountID]; !wasCurrent {
				rt.activeSince[accountID] = switchAt
			}
		}
		rt.switchHistory = append([]AccountLatencyMonitorSwitchRecord{record}, rt.switchHistory...)
		rt.switchHistory = trimAccountLatencyMonitorSwitchHistory(rt.switchHistory)
		activeSince := cloneAccountLatencyMonitorActiveSince(rt.activeSince)
		m.mu.Unlock()
		m.persistRuntimeSwitch(ctx, cfg.GroupID, record, activeSince)
	}
	return nil
}

type accountLatencyMonitorRuntimeState struct {
	LastSwitch    map[string]time.Time                           `json:"last_switch"`
	SwitchHistory map[string][]AccountLatencyMonitorSwitchRecord `json:"switch_history"`
	ActiveSince   map[string]map[string]time.Time                `json:"active_since"`
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
	for groupID, records := range state.SwitchHistory {
		parsed, err := strconv.ParseInt(groupID, 10, 64)
		if err == nil && parsed > 0 {
			rt := m.ensureRuntimeLocked(parsed)
			rt.switchHistory = trimAccountLatencyMonitorSwitchHistory(records)
			if rt.lastSwitch.IsZero() && len(rt.switchHistory) > 0 {
				rt.lastSwitch = rt.switchHistory[0].SwitchedAt
			}
		}
	}
	for groupID, values := range state.ActiveSince {
		parsed, err := strconv.ParseInt(groupID, 10, 64)
		if err != nil || parsed <= 0 {
			continue
		}
		rt := m.ensureRuntimeLocked(parsed)
		rt.activeSince = make(map[int64]time.Time, len(values))
		for accountID, startedAt := range values {
			id, err := strconv.ParseInt(accountID, 10, 64)
			if err == nil && id > 0 && !startedAt.IsZero() {
				rt.activeSince[id] = startedAt
			}
		}
	}
}

func (m *AccountLatencyMonitor) persistRuntimeSwitch(ctx context.Context, groupID int64, record AccountLatencyMonitorSwitchRecord, activeSince map[int64]time.Time) {
	if m == nil || m.settingRepo == nil || groupID <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, accountLatencyMonitorSwitchOperationTimeout)
	defer cancel()
	if !m.lockRuntimePersistence(ctx) {
		return
	}
	defer m.unlockRuntimePersistence()
	state := accountLatencyMonitorRuntimeState{
		LastSwitch:    make(map[string]time.Time),
		SwitchHistory: make(map[string][]AccountLatencyMonitorSwitchRecord),
		ActiveSince:   make(map[string]map[string]time.Time),
	}
	if raw, err := m.settingRepo.GetValue(ctx, SettingKeyAccountLatencyMonitorRuntime); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &state)
		if state.LastSwitch == nil {
			state.LastSwitch = make(map[string]time.Time)
		}
		if state.SwitchHistory == nil {
			state.SwitchHistory = make(map[string][]AccountLatencyMonitorSwitchRecord)
		}
		if state.ActiveSince == nil {
			state.ActiveSince = make(map[string]map[string]time.Time)
		}
	}
	key := strconv.FormatInt(groupID, 10)
	state.LastSwitch[key] = record.SwitchedAt
	state.SwitchHistory[key] = trimAccountLatencyMonitorSwitchHistory(append([]AccountLatencyMonitorSwitchRecord{record}, state.SwitchHistory[key]...))
	state.ActiveSince[key] = accountLatencyMonitorActiveSinceJSON(activeSince)
	payload, err := json.Marshal(state)
	if err == nil {
		_ = m.settingRepo.Set(ctx, SettingKeyAccountLatencyMonitorRuntime, string(payload))
	}
}

func (m *AccountLatencyMonitor) lockRuntimePersistence(ctx context.Context) bool {
	m.mu.Lock()
	if m.runtimePersistGate == nil {
		m.runtimePersistGate = make(chan struct{}, 1)
		m.runtimePersistGate <- struct{}{}
	}
	gate := m.runtimePersistGate
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return false
	case <-gate:
		return true
	}
}

func (m *AccountLatencyMonitor) unlockRuntimePersistence() {
	m.mu.Lock()
	gate := m.runtimePersistGate
	m.mu.Unlock()
	if gate != nil {
		gate <- struct{}{}
	}
}

func cloneAccountLatencyMonitorActiveSince(values map[int64]time.Time) map[int64]time.Time {
	cloned := make(map[int64]time.Time, len(values))
	for accountID, startedAt := range values {
		cloned[accountID] = startedAt
	}
	return cloned
}

func accountLatencyMonitorActiveSinceJSON(values map[int64]time.Time) map[string]time.Time {
	encoded := make(map[string]time.Time, len(values))
	for accountID, startedAt := range values {
		if accountID > 0 && !startedAt.IsZero() {
			encoded[strconv.FormatInt(accountID, 10)] = startedAt
		}
	}
	return encoded
}

func trimAccountLatencyMonitorSwitchHistory(records []AccountLatencyMonitorSwitchRecord) []AccountLatencyMonitorSwitchRecord {
	if len(records) > 100 {
		return append([]AccountLatencyMonitorSwitchRecord(nil), records[:100]...)
	}
	return append([]AccountLatencyMonitorSwitchRecord(nil), records...)
}

func cloneAccountLatencyMonitorSwitchHistory(records []AccountLatencyMonitorSwitchRecord) []AccountLatencyMonitorSwitchRecord {
	cloned := trimAccountLatencyMonitorSwitchHistory(records)
	for i := range cloned {
		cloned[i].PreviousAccountIDs = append([]int64(nil), cloned[i].PreviousAccountIDs...)
		cloned[i].CurrentAccountIDs = append([]int64(nil), cloned[i].CurrentAccountIDs...)
	}
	return cloned
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
			accounts:     make(map[int64]*AccountLatencyMonitorAccountState),
			activeSince:  make(map[int64]time.Time),
			issues:       make(map[int64]map[string][]time.Time),
			recentIssues: make(map[int64][]time.Time),
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
