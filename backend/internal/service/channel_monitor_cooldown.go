package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const ChannelMonitorPriorityExtraKey = "channel_monitor_priority"

// ChannelMonitorCooldownEvent is the account-scoped state returned after a
// failure. Generation lets a late success avoid deleting a newer event.
type ChannelMonitorCooldownEvent struct {
	AccountID  int64
	Generation int64
	Streak     int
	Until      time.Time
}

// ChannelMonitorCooldownStore is deliberately account-only: no model, group,
// provider or credential dimensions are part of this contract.
type ChannelMonitorCooldownStore interface {
	ObserveFailure(context.Context, int64, time.Time, []int) (ChannelMonitorCooldownEvent, error)
	IsCooling(context.Context, int64, time.Time) (bool, error)
	ObserveSuccess(context.Context, int64, int64, time.Time) error
}

type memoryChannelMonitorCooldownState struct{ event ChannelMonitorCooldownEvent }
type memoryChannelMonitorCooldownStore struct {
	mu         sync.Mutex
	states     map[int64]memoryChannelMonitorCooldownState
	generation atomic.Int64
}

func NewMemoryChannelMonitorCooldownStore() *memoryChannelMonitorCooldownStore {
	return &memoryChannelMonitorCooldownStore{states: make(map[int64]memoryChannelMonitorCooldownState)}
}

func (s *memoryChannelMonitorCooldownStore) ObserveFailure(_ context.Context, accountID int64, now time.Time, ladder []int) (ChannelMonitorCooldownEvent, error) {
	if s == nil || accountID <= 0 || len(ladder) == 0 {
		return ChannelMonitorCooldownEvent{}, errors.New("invalid cooldown failure")
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.states[accountID]
	if state.event.AccountID == accountID && state.event.Until.After(now) {
		return state.event, nil
	}
	streak := state.event.Streak + 1
	if streak < 1 {
		streak = 1
	}
	idx := streak - 1
	if idx >= len(ladder) {
		idx = len(ladder) - 1
	}
	event := ChannelMonitorCooldownEvent{AccountID: accountID, Generation: s.generation.Add(1), Streak: streak, Until: now.Add(time.Duration(ladder[idx]) * time.Minute)}
	s.states[accountID] = memoryChannelMonitorCooldownState{event: event}
	return event, nil
}
func (s *memoryChannelMonitorCooldownStore) IsCooling(_ context.Context, accountID int64, now time.Time) (bool, error) {
	if s == nil || accountID <= 0 {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.states[accountID]
	return ok && e.event.Until.After(now.UTC()), nil
}
func (s *memoryChannelMonitorCooldownStore) ObserveSuccess(_ context.Context, accountID, generation int64, now time.Time) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.states[accountID]
	if !ok {
		return nil
	}
	// A terminal success may clear the consecutive streak while an active
	// cooldown remains in force, but it must always identify the event it
	// observed. A zero generation is an unscoped/late result and is ignored.
	if generation > 0 && e.event.Generation == generation {
		if e.event.Until.After(now.UTC()) {
			e.event.Streak = 0
			s.states[accountID] = e
		} else {
			delete(s.states, accountID)
		}
	}
	return nil
}
func (s *memoryChannelMonitorCooldownStore) Current(_ context.Context, accountID int64) (ChannelMonitorCooldownEvent, error) {
	if s == nil {
		return ChannelMonitorCooldownEvent{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[accountID].event, nil
}

// ChannelMonitorProbeOutcome is the one terminal observation for an attempt.
type ChannelMonitorProbeOutcome struct {
	Err            error
	HTTPStatus     int
	Duration       time.Duration
	TransportState ChannelMonitorTransportState
}
type ChannelMonitorProbeAttempt struct {
	AccountID  int64
	StartedAt  time.Time
	generation int64
	monitorID  int64
	requestID  string
	ledger     ChannelMonitorRequestLedgerStore
	state      *channelMonitorProbeLedgerState
	terminal   sync.Once
}

// ChannelMonitorPriorityRepository is the minimal account persistence surface.
type ChannelMonitorPriorityRepository interface {
	GetByID(context.Context, int64) (*Account, error)
	UpdateExtra(context.Context, int64, map[string]any) error
}

// ChannelMonitorPriorityAtomicRepository is implemented by the production
// account repository so concurrent probes across processes share one limit.
type ChannelMonitorPriorityAtomicRepository interface {
	UpdateChannelMonitorPriority(context.Context, int64, int, time.Time, *ChannelMonitorCooldownSettings) (*Account, error)
}

type channelMonitorPriorityAdjuster struct {
	repo  ChannelMonitorPriorityRepository
	cache SchedulerCache
	locks sync.Map
}

func NewChannelMonitorPriorityAdjuster(repo ChannelMonitorPriorityRepository, cache SchedulerCache) *channelMonitorPriorityAdjuster {
	return &channelMonitorPriorityAdjuster{repo: repo, cache: cache}
}

type ChannelMonitorPriorityState struct {
	Boost            int       `json:"boost"`
	Increases        int       `json:"increases"`
	FirstIncreasedAt time.Time `json:"first_increased_at"`
	RecoverySeconds  int       `json:"recovery_seconds,omitempty"`
}

func (a *channelMonitorPriorityAdjuster) lock(id int64) *sync.Mutex {
	v, _ := a.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func (a *channelMonitorPriorityAdjuster) Observe(ctx context.Context, accountID int64, durationSeconds int, now time.Time, cfg *ChannelMonitorCooldownSettings) error {
	if a == nil || a.repo == nil || accountID <= 0 || cfg == nil {
		return nil
	}
	if _, ok := ChannelMonitorProbeFromContext(ctx); !ok {
		return nil
	}
	now = now.UTC()
	if atomicRepo, ok := a.repo.(ChannelMonitorPriorityAtomicRepository); ok {
		account, err := atomicRepo.UpdateChannelMonitorPriority(ctx, accountID, durationSeconds, now, cfg)
		if err != nil {
			return err
		}
		if a.cache != nil && account != nil {
			_ = a.cache.SetAccount(ctx, account)
		}
		return nil
	}
	mu := a.lock(accountID)
	mu.Lock()
	defer mu.Unlock()
	account, err := a.repo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return err
	}
	st := NextChannelMonitorPriorityState(ReadChannelMonitorPriorityState(account.Extra), durationSeconds, now, cfg)
	if err := a.repo.UpdateExtra(ctx, accountID, map[string]any{ChannelMonitorPriorityExtraKey: st}); err != nil {
		return err
	}
	account.Extra = cloneExtra(account.Extra)
	account.Extra[ChannelMonitorPriorityExtraKey] = st
	if a.cache != nil {
		_ = a.cache.SetAccount(ctx, account)
	}
	return nil
}
func (a *channelMonitorPriorityAdjuster) Boost(account *Account) int {
	if account == nil {
		return 0
	}
	return ReadChannelMonitorPriorityState(account.Extra).Boost
}

func (s ChannelMonitorPriorityState) EffectiveBoost(now time.Time) int {
	recovery := s.RecoverySeconds
	if recovery <= 0 {
		recovery = DefaultChannelMonitorCooldownSettings().PriorityAutoRecoverySeconds
	}
	if !s.FirstIncreasedAt.IsZero() && !now.Before(s.FirstIncreasedAt.Add(time.Duration(recovery)*time.Second)) {
		return 0
	}
	return s.Boost
}

func NextChannelMonitorPriorityState(st ChannelMonitorPriorityState, durationSeconds int, now time.Time, cfg *ChannelMonitorCooldownSettings) ChannelMonitorPriorityState {
	if cfg == nil {
		cfg = DefaultChannelMonitorCooldownSettings()
	}
	if st.EffectiveBoost(now) == 0 && st.Boost != 0 {
		st = ChannelMonitorPriorityState{}
	}
	if durationSeconds <= cfg.SlowResponseThresholdSeconds {
		return ChannelMonitorPriorityState{}
	}
	if st.Increases < cfg.MaxPriorityIncrease {
		st.Boost += cfg.PriorityIncrement
		st.Increases++
		if st.FirstIncreasedAt.IsZero() {
			st.FirstIncreasedAt = now.UTC()
			st.RecoverySeconds = cfg.PriorityAutoRecoverySeconds
		}
	}
	return st
}

func ReadChannelMonitorPriorityState(extra map[string]any) ChannelMonitorPriorityState {
	var st ChannelMonitorPriorityState
	if extra == nil {
		return st
	}
	value := extra[ChannelMonitorPriorityExtraKey]
	switch raw := value.(type) {
	case ChannelMonitorPriorityState:
		return raw
	case *ChannelMonitorPriorityState:
		if raw != nil {
			return *raw
		}
	case map[string]any:
		st.Boost = channelMonitorAsInt(raw["boost"])
		st.Increases = channelMonitorAsInt(raw["increases"])
		st.RecoverySeconds = channelMonitorAsInt(raw["recovery_seconds"])
		if v, ok := raw["first_increased_at"].(string); ok {
			st.FirstIncreasedAt, _ = time.Parse(time.RFC3339Nano, v)
		}
		if v, ok := raw["first_increased_at"].(time.Time); ok {
			st.FirstIncreasedAt = v
		}
	}
	return st
}
func channelMonitorAsInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}
func cloneExtra(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ChannelMonitorProbeObserver coordinates exactly one terminal result per
// selected account attempt and keeps ordinary requests on a zero-cost path.
type ChannelMonitorProbeObserver struct {
	store    ChannelMonitorCooldownStore
	priority *channelMonitorPriorityAdjuster
	settings func(context.Context) (*ChannelMonitorCooldownSettings, error)
	ledger   ChannelMonitorRequestLedgerStore
}

type channelMonitorCooldownGenerationReader interface {
	Current(context.Context, int64) (ChannelMonitorCooldownEvent, error)
}

func NewChannelMonitorProbeObserver(store ChannelMonitorCooldownStore, priority *channelMonitorPriorityAdjuster, settings func(context.Context) (*ChannelMonitorCooldownSettings, error)) *ChannelMonitorProbeObserver {
	if settings == nil {
		settings = func(context.Context) (*ChannelMonitorCooldownSettings, error) {
			return DefaultChannelMonitorCooldownSettings(), nil
		}
	}
	return &ChannelMonitorProbeObserver{store: store, priority: priority, settings: settings}
}

// SetRequestLedger installs the shared Redis request-attempt ledger. Keeping
// this setter preserves the constructor contract used by existing callers and
// tests while allowing the server wire to share one store across gateways and
// the checker service.
func (o *ChannelMonitorProbeObserver) SetRequestLedger(ledger ChannelMonitorRequestLedgerStore) {
	if o != nil {
		o.ledger = ledger
	}
}

func (o *ChannelMonitorProbeObserver) disableLedger(ctx context.Context, attempt *ChannelMonitorProbeAttempt, stage string, err error) {
	if attempt == nil {
		return
	}
	if attempt.state != nil {
		attempt.state.disabled.Store(true)
	}
	slog.ErrorContext(ctx, "channel monitor cooldown request disabled",
		"monitor_id", attempt.monitorID,
		"request_id", attempt.requestID,
		"stage", stage,
		"error", err,
	)
}

// ChannelMonitorCooldownSettingsProvider exposes the validated runtime policy
// with the narrow signature expected by the probe observer.
func (s *SettingService) ChannelMonitorCooldownSettingsProvider(ctx context.Context) (*ChannelMonitorCooldownSettings, error) {
	return s.GetChannelMonitorCooldownSettings(ctx)
}
func (o *ChannelMonitorProbeObserver) Begin(ctx context.Context, account *Account, started time.Time) *ChannelMonitorProbeAttempt {
	if o == nil || account == nil || account.ID <= 0 {
		return nil
	}
	if _, ok := ChannelMonitorProbeFromContext(ctx); !ok {
		return nil
	}
	// This capability is intentionally limited to direct API-key accounts.
	// OAuth, Setup Token, pool-mode and other account types retain existing behavior.
	if account.Type != AccountTypeAPIKey || account.IsPoolMode() {
		return nil
	}
	probe, ok := ChannelMonitorProbeFromContext(ctx)
	if !ok {
		return nil
	}
	state := channelMonitorProbeLedgerStateFromContext(ctx)
	if state != nil && state.disabled.Load() {
		return nil
	}
	attempt := &ChannelMonitorProbeAttempt{AccountID: account.ID, StartedAt: started, monitorID: probe.MonitorID, requestID: probe.RequestID, ledger: o.ledger, state: state}
	if attempt.ledger == nil {
		o.disableLedger(ctx, attempt, "ledger_unconfigured", errors.New("request ledger is not configured"))
		return nil
	}
	if reader, ok := o.store.(channelMonitorCooldownGenerationReader); ok {
		event, err := reader.Current(ctx, account.ID)
		if err != nil {
			o.disableLedger(ctx, attempt, "read_generation", err)
			return nil
		}
		attempt.generation = event.Generation
	}
	operationCtx, cancel := channelMonitorLedgerOperationContext(ctx, false)
	defer cancel()
	if _, err := attempt.ledger.RegisterAttempt(operationCtx, attempt.monitorID, attempt.requestID, attempt.AccountID, attempt.generation, ChannelMonitorRequestLedgerTTL); err != nil {
		o.disableLedger(ctx, attempt, "register_attempt", err)
		return nil
	}
	return attempt
}
func (o *ChannelMonitorProbeObserver) Finish(ctx context.Context, attempt *ChannelMonitorProbeAttempt, outcome ChannelMonitorProbeOutcome) {
	if o == nil || attempt == nil {
		return
	}
	attempt.terminal.Do(func() {
		// A valid monitor probe is an internal health check. Its HTTP client
		// may cancel the server request when the response-header timeout fires;
		// that cancellation is the probe's failure signal and must enter the
		// cooldown chain. Ordinary user-request cancellation still bypasses it.
		_, isProbe := ChannelMonitorProbeFromContext(ctx)
		if !isProbe && (errors.Is(outcome.Err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled))) {
			return
		}
		// The upstream may ignore the canceled request context and return a
		// late success after the monitor client has already timed out. Preserve
		// the monitor's observed failure instead of allowing that late success
		// to clear or skip the cooldown event.
		if isProbe && outcome.Err == nil && ctx != nil && ctx.Err() != nil {
			outcome.Err = ctx.Err()
		}
		now := time.Now().UTC()
		if outcome.Duration <= 0 && !attempt.StartedAt.IsZero() {
			outcome.Duration = now.Sub(attempt.StartedAt)
		}
		if attempt.state != nil && attempt.state.disabled.Load() {
			return
		}
		state := outcome.TransportState
		if outcome.Err != nil {
			state = ChannelMonitorTransportFailed
		} else if state == "" {
			state = ChannelMonitorTransportSucceeded
		}
		operationCtx, cancel := channelMonitorLedgerOperationContext(ctx, true)
		defer cancel()
		if err := attempt.ledger.RecordTransport(operationCtx, attempt.monitorID, attempt.requestID, attempt.AccountID, state, int(outcome.Duration/time.Second)); err != nil {
			o.disableLedger(ctx, attempt, "record_transport", err)
		}
	})
}

// Finalize atomically attributes the final checker result to every complete
// target attempt in the request ledger. Errors are diagnostic-only: monitor
// status and the original gateway response are never changed.
func (o *ChannelMonitorProbeObserver) Finalize(ctx context.Context, monitorID int64, requestID, finalStatus string) {
	if o == nil || o.ledger == nil || monitorID <= 0 || requestID == "" {
		return
	}
	operationCtx, cancel := channelMonitorLedgerOperationContext(ctx, true)
	defer cancel()
	cfg, _ := o.settings(operationCtx)
	if cfg == nil {
		cfg = DefaultChannelMonitorCooldownSettings()
	}
	now := time.Now().UTC()
	result, err := o.ledger.Finalize(operationCtx, monitorID, requestID, finalStatus, now, cfg.CooldownMinutes)
	if err != nil {
		slog.ErrorContext(ctx, "channel monitor cooldown request disabled",
			"monitor_id", monitorID,
			"request_id", requestID,
			"stage", "final_attribution",
			"error", err,
		)
		return
	}
	if !result.Claimed || o.priority == nil {
		return
	}
	probeCtx := WithChannelMonitorProbe(operationCtx, ChannelMonitorProbe{MonitorID: monitorID, RequestID: requestID})
	for _, attempt := range result.Ledger.Attempts {
		if err := o.priority.Observe(probeCtx, attempt.AccountID, attempt.DurationSeconds, now, cfg); err != nil {
			slog.ErrorContext(ctx, "channel monitor priority observation failed",
				"monitor_id", monitorID,
				"request_id", requestID,
				"account_id", attempt.AccountID,
				"stage", "final_priority",
				"error", err,
			)
		}
	}
}

func channelMonitorLedgerOperationContext(ctx context.Context, detach bool) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	} else if detach {
		ctx = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(ctx, 500*time.Millisecond)
}

func (s ChannelMonitorCooldownSettings) Validate() error {
	_, err := NormalizeChannelMonitorCooldownSettings(&s)
	return err
}
