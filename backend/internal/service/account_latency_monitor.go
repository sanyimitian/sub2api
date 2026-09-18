package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	accountLatencyMonitorDefaultModel       = "gpt-5.6-sol"
	accountLatencyMonitorDefaultPrompt      = "hi"
	accountLatencyMonitorSettingsRefresh    = 5 * time.Second
	accountLatencyMonitorDefaultThreshold   = 10
	accountLatencyMonitorDefaultWindow      = 30
	accountLatencyMonitorDefaultFailures    = 2
	accountLatencyMonitorDefaultProbePeriod = 60
	accountLatencyMonitorDefaultBackupCount = 2
	accountLatencyMonitorDefaultTimeout     = 30
	accountLatencyMonitorDefaultConcurrency = 4
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
	GroupID             int64   `json:"group_id"`
	Enabled             bool    `json:"enabled"`
	LatencyThresholdSec int     `json:"latency_threshold_seconds"`
	FailureWindowSec    int     `json:"failure_window_seconds"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	ProbeIntervalSec    int     `json:"probe_interval_seconds"`
	ProbeTimeoutSec     int     `json:"probe_timeout_seconds"`
	ProbeConcurrency    int     `json:"probe_concurrency"`
	BackupCount         int     `json:"backup_count"`
	AlwaysEnabledIDs    []int64 `json:"always_enabled_account_ids"`
	ProbeModel          string  `json:"probe_model"`
	ProbePrompt         string  `json:"probe_prompt"`
	ProbeReasoning      string  `json:"probe_reasoning_effort"`
}

type AccountLatencyMonitorAccountState struct {
	AccountID          int64      `json:"account_id"`
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
	Accounts         []AccountLatencyMonitorAccountState `json:"accounts"`
}

type accountLatencyMonitorGroupRuntime struct {
	accounts  map[int64]*AccountLatencyMonitorAccountState
	backups   []int64
	lastProbe time.Time
	probing   bool
	switching bool
	issues    map[int64]accountLatencyMonitorIssue
}

type accountLatencyMonitorIssue struct {
	kind      string
	startedAt time.Time
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

	settingsMu     sync.RWMutex
	cachedSettings AccountLatencyMonitorSettings
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
		GroupID:             groupID,
		Enabled:             true,
		LatencyThresholdSec: accountLatencyMonitorDefaultThreshold,
		FailureWindowSec:    accountLatencyMonitorDefaultWindow,
		ConsecutiveFailures: accountLatencyMonitorDefaultFailures,
		ProbeIntervalSec:    accountLatencyMonitorDefaultProbePeriod,
		ProbeTimeoutSec:     accountLatencyMonitorDefaultTimeout,
		ProbeConcurrency:    accountLatencyMonitorDefaultConcurrency,
		BackupCount:         accountLatencyMonitorDefaultBackupCount,
		ProbeModel:          accountLatencyMonitorDefaultModel,
		ProbePrompt:         accountLatencyMonitorDefaultPrompt,
		ProbeReasoning:      "low",
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
	if g.ProbeTimeoutSec <= 0 {
		g.ProbeTimeoutSec = accountLatencyMonitorDefaultTimeout
	}
	if g.ProbeConcurrency <= 0 {
		g.ProbeConcurrency = accountLatencyMonitorDefaultConcurrency
	}
	if g.BackupCount <= 0 {
		g.BackupCount = accountLatencyMonitorDefaultBackupCount
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

// RecordRequest reports one user request. Two same-kind failures within the
// configured window trigger an immediate switch to a previously tested backup.
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
	state.LastSuccess = accountLatencyMonitorBoolPtr(success)
	state.LastObservedAt = accountLatencyMonitorTimePtr(now)
	if firstTokenMs == nil {
		state.LastLatencyMs = nil
	} else {
		state.LastLatencyMs = accountLatencyMonitorInt64Ptr(int64(*firstTokenMs))
	}

	shouldFailover := false
	if issue == "" {
		state.ConsecutiveFailure = 0
		delete(rt.issues, accountID)
	} else {
		previous, found := rt.issues[accountID]
		withinWindow := found && previous.kind == issue && now.Sub(previous.startedAt) <= time.Duration(cfg.FailureWindowSec)*time.Second
		if withinWindow {
			state.ConsecutiveFailure++
		} else {
			state.ConsecutiveFailure = 1
			rt.issues[accountID] = accountLatencyMonitorIssue{kind: issue, startedAt: now}
		}
		if state.ConsecutiveFailure >= cfg.ConsecutiveFailures && !rt.switching {
			rt.switching = true
			shouldFailover = true
		}
	}
	m.mu.Unlock()

	if shouldFailover {
		go m.failover(context.Background(), cfg, accountID)
	}
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

func (m *AccountLatencyMonitor) run() {
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
			go m.probeAllAndFinish(context.Background(), cfg, nil, true)
		}
	}
}

func (m *AccountLatencyMonitor) startDueProbe(cfg AccountLatencyMonitorGroup) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.ensureRuntimeLocked(cfg.GroupID)
	due := rt.lastProbe.IsZero() || time.Since(rt.lastProbe) >= time.Duration(cfg.ProbeIntervalSec)*time.Second
	if !due || rt.probing {
		return false
	}
	rt.probing = true
	return true
}

func (m *AccountLatencyMonitor) hasBackups(groupID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.ensureRuntimeLocked(groupID).backups) > 0
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
		_ = m.setSchedulableAccounts(ctx, cfg, []int64{backupID})
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
	byID := make(map[int64]Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	backups := make([]Account, 0, len(backupIDs))
	for _, accountID := range backupIDs {
		if account, ok := byID[accountID]; ok {
			backups = append(backups, account)
		}
	}
	if len(backups) != len(backupIDs) {
		m.probeAll(ctx, cfg, nil, true)
		return
	}

	results := m.probeAccounts(ctx, cfg, backups)
	thresholdMs := int64(cfg.LatencyThresholdSec * 1000)
	for _, result := range results {
		if !result.success || result.latency >= thresholdMs {
			m.probeAll(ctx, cfg, nil, true)
			return
		}
	}
}

func (m *AccountLatencyMonitor) probeAllAndFinish(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate bool) {
	defer m.finishProbe(cfg.GroupID)
	m.probeAll(ctx, cfg, excluded, activate)
}

func (m *AccountLatencyMonitor) probeAll(ctx context.Context, cfg AccountLatencyMonitorGroup, excluded map[int64]struct{}, activate bool) {
	accounts, err := m.listGroupAccounts(ctx, cfg.GroupID)
	if err != nil {
		return
	}
	results := m.probeAccounts(ctx, cfg, accounts)
	chosen := selectAccountLatencyMonitorBackups(results, cfg.GroupID, int64(cfg.LatencyThresholdSec*1000), cfg.BackupCount, excluded)
	m.mu.Lock()
	m.ensureRuntimeLocked(cfg.GroupID).backups = append([]int64(nil), chosen...)
	m.mu.Unlock()
	if len(chosen) == 0 {
		return
	}
	if activate {
		_ = m.setSchedulableAccounts(ctx, cfg, chosen[:1])
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

func accountLatencyMonitorPriority(account Account, groupID int64) int {
	for _, group := range account.AccountGroups {
		if group.GroupID == groupID {
			return group.Priority
		}
	}
	return account.Priority
}

func (m *AccountLatencyMonitor) backupIDs(groupID int64) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int64(nil), m.ensureRuntimeLocked(groupID).backups...)
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
		if account.Schedulable != wantSchedulable {
			if err := m.accountRepo.SetSchedulable(ctx, account.ID, wantSchedulable); err != nil {
				return err
			}
		}
	}
	return nil
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
			issues:   make(map[int64]accountLatencyMonitorIssue),
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
