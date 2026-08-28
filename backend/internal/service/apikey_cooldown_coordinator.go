package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// APIKeyCooldownSettingsProvider supplies the current validated runtime policy.
// It is deliberately narrower than SettingService so the coordinator remains
// easy to exercise with deterministic test providers.
type APIKeyCooldownSettingsProvider interface {
	GetAPIKeyFailureCooldownSettings(context.Context) (*APIKeyFailureCooldownSettings, error)
}

// APIKeyCooldownStateWriter is the small persistence surface needed by the
// coordinator. AccountRepository implements it, while narrow fakes can test
// cooldown behavior without implementing unrelated account operations.
type APIKeyCooldownStateWriter interface {
	SetTempUnschedulable(context.Context, int64, time.Time, string) error
	SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error
}

// APIKeyCooldownCoordinator is the single state transition entry point for
// direct, non-pool API-key accounts.
type APIKeyCooldownCoordinator struct {
	accountRepo APIKeyCooldownStateWriter
	store       APIKeyCooldownStore
	settings    APIKeyCooldownSettingsProvider
	localMu     sync.Mutex
	localEvents map[string]APIKeyCooldownEvent
	attempts    map[string]apiKeyCooldownAttemptResult
}

type apiKeyCooldownAttemptResult struct {
	decision  APIKeyCooldownDecision
	expiresAt time.Time
}

func NewAPIKeyCooldownCoordinator(accountRepo APIKeyCooldownStateWriter, store APIKeyCooldownStore, settings APIKeyCooldownSettingsProvider) *APIKeyCooldownCoordinator {
	return &APIKeyCooldownCoordinator{
		accountRepo: accountRepo, store: store, settings: settings,
		localEvents: make(map[string]APIKeyCooldownEvent), attempts: make(map[string]apiKeyCooldownAttemptResult),
	}
}

func (c *APIKeyCooldownCoordinator) ObserveFailure(ctx context.Context, account *Account, observation APIKeyFailureObservation, now time.Time) (APIKeyCooldownDecision, error) {
	decision := ClassifyAPIKeyFailure(observation)
	if c == nil || account == nil || !IsAPIKeyFailureCooldownApplicable(account) || !decision.ShouldCooldown() {
		return decision, nil
	}
	if account.ID <= 0 {
		return APIKeyCooldownDecision{Disposition: APIKeyCooldownDispositionIgnored}, nil
	}
	now = now.UTC()
	if previous, ok := c.attemptResult(account.ID, observation.AttemptID, now); ok {
		return previous, nil
	}
	settings := DefaultAPIKeyFailureCooldownSettings()
	if c.settings != nil {
		if loaded, err := c.settings.GetAPIKeyFailureCooldownSettings(ctx); err == nil && loaded != nil {
			settings = loaded
		}
	}
	policy, enabled := ResolveAPIKeyCooldownPolicy(settings, decision.Family)
	if !enabled {
		return APIKeyCooldownDecision{Disposition: APIKeyCooldownDispositionIgnored, Family: decision.Family, Scope: decision.Scope}, nil
	}
	key := APIKeyCooldownKey{
		AccountID: account.ID,
		Model:     decisionModelForScope(decision.Scope, observation.Model),
		Family:    decision.Family,
		Scope:     decisionScopeForObservation(decision.Scope, observation.Model),
	}
	if key.Scope == APIKeyCooldownScopeModel && key.NormalizedModel() == "" {
		key.Scope = APIKeyCooldownScopeAccount
		key.Model = ""
	}
	var event APIKeyCooldownEvent
	var err error
	if c.store != nil {
		event, err = c.store.ObserveFailure(ctx, key, policy, now, observation.UpstreamReset)
	}
	if err != nil || c.store == nil {
		// A dependency failure must not remove the first-tier local protection.
		fallback := CalculateAPIKeyCooldownDuration(settings, decision.Family, 1, now, observation.UpstreamReset)
		if fallback <= 0 {
			fallback = time.Second
		}
		event = APIKeyCooldownEvent{Key: key, Generation: 0, Streak: 1, Until: now.Add(fallback), Created: true, NeedsPersistence: true}
		if persistErr := c.persist(ctx, account, event, decision, observation, now); persistErr != nil {
			slog.Warn("api_key_cooldown_fallback_persist_failed", "account_id", account.ID, "family", decision.Family, "error", persistErr)
		}
		c.rememberLocalEvent(event)
		decision.Streak = event.Streak
		decision.Generation = event.Generation
		decision.Until = event.Until
		decision.Exclude = true
		decision.Reason = cooldownReason(decision, observation, event)
		c.rememberAttemptResult(account.ID, observation.AttemptID, decision, now)
		return decision, nil
	}
	decision.Streak = event.Streak
	decision.Generation = event.Generation
	decision.Until = event.Until
	decision.Exclude = true
	decision.Reason = cooldownReason(decision, observation, event)
	if event.NeedsPersistence {
		if persistErr := c.persist(ctx, account, event, decision, observation, now); persistErr != nil {
			slog.Warn("api_key_cooldown_persist_failed", "account_id", account.ID, "family", decision.Family, "error", persistErr)
		} else if markErr := c.store.MarkPersisted(ctx, event.Key, event.Generation); markErr != nil {
			slog.Warn("api_key_cooldown_mark_persisted_failed", "account_id", account.ID, "family", decision.Family, "error", markErr)
		}
	}
	c.rememberAttemptResult(account.ID, observation.AttemptID, decision, now)
	return decision, nil
}

func (c *APIKeyCooldownCoordinator) Check(ctx context.Context, account *Account, model string, now time.Time) (bool, APIKeyCooldownAttemptToken, error) {
	token := APIKeyCooldownAttemptToken{StartedAt: now.UTC(), Generations: make(map[string]int64)}
	if c == nil || account == nil || !IsAPIKeyFailureCooldownApplicable(account) || c.store == nil {
		if c != nil && account != nil {
			if event, ok := c.localEvent(account.ID, model, now); ok {
				if event.Generation > 0 {
					token.Generations[event.Key.RedisKey()] = event.Generation
				}
				return true, token, nil
			}
		}
		return false, token, nil
	}
	settings := DefaultAPIKeyFailureCooldownSettings()
	if c.settings != nil {
		if loaded, err := c.settings.GetAPIKeyFailureCooldownSettings(ctx); err == nil && loaded != nil {
			settings = loaded
		}
	}
	for _, family := range allAPIKeyFailureFamilies {
		policy, enabled := ResolveAPIKeyCooldownPolicy(settings, family)
		if !enabled {
			continue
		}
		for _, scope := range cooldownGuardScopes(family, model) {
			key := APIKeyCooldownKey{AccountID: account.ID, Model: model, Family: family, Scope: scope}
			event, active, err := c.store.Check(ctx, key, now.UTC())
			if err != nil {
				if event, ok := c.localEvent(account.ID, model, now); ok {
					if event.Generation > 0 {
						token.Generations[event.Key.RedisKey()] = event.Generation
					}
					return true, token, nil
				}
				continue
			}
			if event.Generation > 0 {
				token.Generations[key.RedisKey()] = event.Generation
			}
			if active {
				return true, token, nil
			}
		}
		_ = policy
	}
	return false, token, nil
}

func cooldownGuardScopes(family APIKeyFailureFamily, model string) []APIKeyCooldownScope {
	scopes := []APIKeyCooldownScope{APIKeyCooldownScopeAccount}
	if family.DefaultScope() == APIKeyCooldownScopeModel && NormalizeAPIKeyCooldownModel(model) != "" {
		scopes = append(scopes, APIKeyCooldownScopeModel)
	}
	return scopes
}

func (c *APIKeyCooldownCoordinator) attemptResult(accountID int64, attemptID string, now time.Time) (APIKeyCooldownDecision, bool) {
	attemptID = strings.TrimSpace(attemptID)
	if c == nil || accountID <= 0 || attemptID == "" {
		return APIKeyCooldownDecision{}, false
	}
	c.localMu.Lock()
	defer c.localMu.Unlock()
	key := fmt.Sprintf("%d:%s", accountID, attemptID)
	result, ok := c.attempts[key]
	if ok && result.expiresAt.After(now.UTC()) {
		return result.decision, true
	}
	if ok {
		delete(c.attempts, key)
	}
	return APIKeyCooldownDecision{}, false
}

func (c *APIKeyCooldownCoordinator) rememberAttemptResult(accountID int64, attemptID string, decision APIKeyCooldownDecision, now time.Time) {
	attemptID = strings.TrimSpace(attemptID)
	if c == nil || accountID <= 0 || attemptID == "" {
		return
	}
	c.localMu.Lock()
	defer c.localMu.Unlock()
	if c.attempts == nil {
		c.attempts = make(map[string]apiKeyCooldownAttemptResult)
	}
	c.attempts[fmt.Sprintf("%d:%s", accountID, attemptID)] = apiKeyCooldownAttemptResult{
		decision: decision, expiresAt: now.UTC().Add(24 * time.Hour),
	}
}

func (c *APIKeyCooldownCoordinator) ObserveSuccess(ctx context.Context, account *Account, success APIKeyCooldownSuccess, now time.Time) error {
	if c == nil || c.store == nil || account == nil || !IsAPIKeyFailureCooldownApplicable(account) {
		return nil
	}
	keys := make([]APIKeyCooldownKey, 0, len(allAPIKeyFailureFamilies))
	for _, family := range allAPIKeyFailureFamilies {
		keys = append(keys,
			APIKeyCooldownKey{AccountID: account.ID, Family: family, Scope: APIKeyCooldownScopeAccount},
			APIKeyCooldownKey{AccountID: account.ID, Model: success.Model, Family: family, Scope: APIKeyCooldownScopeModel},
		)
	}
	return c.store.ResetSuccess(ctx, keys, success.AttemptToken, now.UTC())
}

func (c *APIKeyCooldownCoordinator) persist(ctx context.Context, account *Account, event APIKeyCooldownEvent, decision APIKeyCooldownDecision, observation APIKeyFailureObservation, now time.Time) error {
	if c.accountRepo == nil || account == nil || !event.Until.After(now.UTC()) {
		return nil
	}
	reason := cooldownReason(decision, observation, event)
	if event.Key.Scope == APIKeyCooldownScopeModel {
		return c.accountRepo.SetModelRateLimit(ctx, account.ID, event.Key.NormalizedModel(), event.Until, reason)
	}
	return c.accountRepo.SetTempUnschedulable(ctx, account.ID, event.Until, reason)
}

func (c *APIKeyCooldownCoordinator) rememberLocalEvent(event APIKeyCooldownEvent) {
	if c == nil || event.Key.AccountID <= 0 {
		return
	}
	c.localMu.Lock()
	defer c.localMu.Unlock()
	if c.localEvents == nil {
		c.localEvents = make(map[string]APIKeyCooldownEvent)
	}
	key := event.Key.RedisKey()
	if current, ok := c.localEvents[key]; !ok || current.Until.Before(event.Until) {
		c.localEvents[key] = event
	}
}

func (c *APIKeyCooldownCoordinator) localEvent(accountID int64, model string, now time.Time) (APIKeyCooldownEvent, bool) {
	if c == nil || accountID <= 0 {
		return APIKeyCooldownEvent{}, false
	}
	c.localMu.Lock()
	defer c.localMu.Unlock()
	for _, family := range allAPIKeyFailureFamilies {
		for _, scope := range []APIKeyCooldownScope{APIKeyCooldownScopeAccount, APIKeyCooldownScopeModel} {
			key := APIKeyCooldownKey{AccountID: accountID, Model: model, Family: family, Scope: scope}
			if scope == APIKeyCooldownScopeModel && key.NormalizedModel() == "" {
				continue
			}
			event, ok := c.localEvents[key.RedisKey()]
			if ok && event.Until.After(now.UTC()) {
				return event, true
			}
			if ok && !event.Until.After(now.UTC()) {
				delete(c.localEvents, key.RedisKey())
			}
		}
	}
	return APIKeyCooldownEvent{}, false
}

func decisionModelForScope(scope APIKeyCooldownScope, model string) string {
	if scope != APIKeyCooldownScopeModel {
		return ""
	}
	return NormalizeAPIKeyCooldownModel(model)
}

func decisionScopeForObservation(scope APIKeyCooldownScope, model string) APIKeyCooldownScope {
	if scope == APIKeyCooldownScopeModel && NormalizeAPIKeyCooldownModel(model) != "" {
		return APIKeyCooldownScopeModel
	}
	return APIKeyCooldownScopeAccount
}

func cooldownReason(decision APIKeyCooldownDecision, observation APIKeyFailureObservation, event APIKeyCooldownEvent) string {
	payload := map[string]any{
		"source":     "api_key_failure_cooldown",
		"family":     string(decision.Family),
		"scope":      string(event.Key.Scope),
		"streak":     event.Streak,
		"generation": event.Generation,
		"until":      event.Until.UTC().Format(time.RFC3339),
		"status":     observation.HTTPStatus,
	}
	if summary := strings.TrimSpace(SanitizeAPIKeyErrorSummary(observation.ErrorSummary)); summary != "" {
		payload["summary"] = summary
	}
	if model := event.Key.NormalizedModel(); model != "" {
		payload["model"] = model
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("api_key_failure_cooldown:%s", decision.Family)
	}
	return string(raw)
}
