package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

type cooldownCoordinatorRepoStub struct {
	account         *Account
	setTempCalls    int
	setModelCalls   int
	lastTempUntil   time.Time
	lastTempReason  string
	lastModel       string
	lastModelUntil  time.Time
	lastModelReason string
	setTempErr      error
	setModelErr     error
}

type staticAPIKeyCooldownSettingsProvider struct {
	settings *APIKeyFailureCooldownSettings
}

func (p staticAPIKeyCooldownSettingsProvider) GetAPIKeyFailureCooldownSettings(context.Context) (*APIKeyFailureCooldownSettings, error) {
	return cloneAPIKeyFailureCooldownSettings(p.settings), nil
}

func newTestAPIKeyCooldownSettingService(t *testing.T, settings *APIKeyFailureCooldownSettings) APIKeyCooldownSettingsProvider {
	t.Helper()
	return staticAPIKeyCooldownSettingsProvider{settings: settings}
}

func (r *cooldownCoordinatorRepoStub) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, reason string) error {
	r.setTempCalls++
	r.lastTempUntil = until
	r.lastTempReason = reason
	return r.setTempErr
}

func (r *cooldownCoordinatorRepoStub) SetModelRateLimit(_ context.Context, _ int64, model string, until time.Time, reason ...string) error {
	r.setModelCalls++
	r.lastModel = model
	r.lastModelUntil = until
	if len(reason) > 0 {
		r.lastModelReason = reason[0]
	}
	return r.setModelErr
}

func TestAPIKeyCooldownCoordinatorObservesAndPersistsOnlyCreatedEvent(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	account := &Account{ID: 9, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	repo := &cooldownCoordinatorRepoStub{account: account}
	store := NewMemoryAPIKeyCooldownStore()
	settings := DefaultAPIKeyFailureCooldownSettings()
	settings.Policies[APIKeyFailureTransientUpstream] = APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{30, 120}, Mode: APIKeyCooldownModeHoldLast}
	settingSvc := newTestAPIKeyCooldownSettingService(t, settings)
	coordinator := NewAPIKeyCooldownCoordinator(repo, store, settingSvc)

	obs := APIKeyFailureObservation{
		AttemptID:      "attempt-1",
		AttemptStarted: now,
		AccountID:      account.ID,
		AccountType:    account.Type,
		Platform:       account.Platform,
		HTTPStatus:     http.StatusBadGateway,
		RequestSent:    true,
		ReplaySafe:     true,
		ErrorSummary:   "upstream unavailable",
	}
	decision, err := coordinator.ObserveFailure(context.Background(), account, obs, now)
	if err != nil {
		t.Fatalf("ObserveFailure() error = %v", err)
	}
	if !decision.ShouldCooldown() || decision.Streak != 1 || !decision.Exclude {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if repo.setTempCalls != 1 || !repo.lastTempUntil.Equal(now.Add(30*time.Second)) {
		t.Fatalf("expected one first-tier account write, calls=%d until=%v", repo.setTempCalls, repo.lastTempUntil)
	}

	merged, err := coordinator.ObserveFailure(context.Background(), account, obs, now.Add(time.Second))
	if err != nil {
		t.Fatalf("merged ObserveFailure() error = %v", err)
	}
	if merged.Generation != decision.Generation || merged.Streak != decision.Streak {
		t.Fatalf("active event was not merged: first=%+v merged=%+v", decision, merged)
	}
	if repo.setTempCalls != 1 {
		t.Fatalf("active merged failure must not repeat persistence, calls=%d", repo.setTempCalls)
	}
}

func TestAPIKeyCooldownCoordinatorModelScopeAndStructuredReason(t *testing.T) {
	now := time.Unix(11_000, 0).UTC()
	account := &Account{ID: 10, Type: AccountTypeAPIKey, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}
	repo := &cooldownCoordinatorRepoStub{account: account}
	coordinator := NewAPIKeyCooldownCoordinator(repo, NewMemoryAPIKeyCooldownStore(), newTestAPIKeyCooldownSettingService(t, DefaultAPIKeyFailureCooldownSettings()))
	decision, err := coordinator.ObserveFailure(context.Background(), account, APIKeyFailureObservation{
		AccountID: account.ID, AccountType: account.Type, Platform: account.Platform,
		Model: " Claude-Sonnet-4 ", HTTPStatus: http.StatusNotFound,
		ErrorCode: "model_not_found", ErrorSummary: "model unavailable", RequestSent: true,
	}, now)
	if err != nil {
		t.Fatalf("ObserveFailure() error = %v", err)
	}
	if decision.Scope != APIKeyCooldownScopeModel || repo.setModelCalls != 1 {
		t.Fatalf("expected model scoped write: decision=%+v calls=%d", decision, repo.setModelCalls)
	}
	if repo.lastModel != "claude-sonnet-4" {
		t.Fatalf("model was not normalized: %q", repo.lastModel)
	}
	var reason map[string]any
	if err := json.Unmarshal([]byte(repo.lastModelReason), &reason); err != nil {
		t.Fatalf("reason is not structured JSON: %v (%q)", err, repo.lastModelReason)
	}
	if reason["family"] != string(APIKeyFailureModelUnsupported) || strings.Contains(repo.lastModelReason, "model unavailable") == false {
		t.Fatalf("unexpected structured reason: %s", repo.lastModelReason)
	}
}

func TestAPIKeyCooldownCoordinatorFallsBackToFirstTierWhenStoreFails(t *testing.T) {
	now := time.Unix(12_000, 0).UTC()
	account := &Account{ID: 11, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	repo := &cooldownCoordinatorRepoStub{account: account}
	store := &failingAPIKeyCooldownStore{err: errors.New("redis down")}
	coordinator := NewAPIKeyCooldownCoordinator(repo, store, newTestAPIKeyCooldownSettingService(t, DefaultAPIKeyFailureCooldownSettings()))
	decision, err := coordinator.ObserveFailure(context.Background(), account, APIKeyFailureObservation{
		AccountID: account.ID, AccountType: account.Type, Platform: account.Platform,
		HTTPStatus: http.StatusBadGateway, RequestSent: true,
	}, now)
	if err != nil {
		t.Fatalf("fallback must not fail request: %v", err)
	}
	if !decision.ShouldCooldown() || !decision.Exclude || decision.Until.Sub(now) != 30*time.Second {
		t.Fatalf("unexpected first-tier fallback decision: %+v", decision)
	}
	if repo.setTempCalls != 1 {
		t.Fatalf("fallback should persist account cooldown once, calls=%d", repo.setTempCalls)
	}
	blocked, token, err := coordinator.Check(context.Background(), account, "", now.Add(time.Second))
	if err != nil || !blocked || len(token.Generations) != 0 {
		t.Fatalf("local fallback guard mismatch: blocked=%v token=%+v err=%v", blocked, token, err)
	}
}

func TestAPIKeyCooldownCoordinatorSuccessUsesAttemptToken(t *testing.T) {
	now := time.Unix(13_000, 0).UTC()
	account := &Account{ID: 12, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	repo := &cooldownCoordinatorRepoStub{account: account}
	store := NewMemoryAPIKeyCooldownStore()
	coordinator := NewAPIKeyCooldownCoordinator(repo, store, newTestAPIKeyCooldownSettingService(t, DefaultAPIKeyFailureCooldownSettings()))
	first, err := coordinator.ObserveFailure(context.Background(), account, APIKeyFailureObservation{
		AccountID: account.ID, AccountType: account.Type, Platform: account.Platform,
		HTTPStatus: http.StatusBadGateway, RequestSent: true,
	}, now)
	if err != nil {
		t.Fatalf("ObserveFailure() error = %v", err)
	}
	_, token, err := coordinator.Check(context.Background(), account, "", now.Add(time.Second))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if err := coordinator.ObserveSuccess(context.Background(), account, APIKeyCooldownSuccess{AccountID: account.ID, AttemptToken: token}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("active success should be accepted without clearing: %v", err)
	}
	if _, active, err := store.Check(context.Background(), APIKeyCooldownKey{AccountID: account.ID, Family: APIKeyFailureTransientUpstream, Scope: APIKeyCooldownScopeAccount}, now.Add(2*time.Second)); err != nil || !active {
		t.Fatalf("active event was cleared too early: active=%v err=%v", active, err)
	}

	if err := coordinator.ObserveSuccess(context.Background(), account, APIKeyCooldownSuccess{AccountID: account.ID, AttemptToken: APIKeyCooldownAttemptToken{Generations: map[string]int64{}}}, first.Until.Add(time.Second)); err != nil {
		t.Fatalf("stale success should be ignored: %v", err)
	}
}

func TestAPIKeyCooldownCoordinatorRetriesPersistenceWithoutAdvancingEvent(t *testing.T) {
	now := time.Unix(14_000, 0).UTC()
	account := &Account{ID: 13, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	repo := &cooldownCoordinatorRepoStub{setTempErr: errors.New("database down")}
	coordinator := NewAPIKeyCooldownCoordinator(repo, NewMemoryAPIKeyCooldownStore(), newTestAPIKeyCooldownSettingService(t, DefaultAPIKeyFailureCooldownSettings()))

	first, err := coordinator.ObserveFailure(context.Background(), account, APIKeyFailureObservation{
		AttemptID: "attempt-1", AccountID: account.ID, AccountType: account.Type,
		Platform: account.Platform, HTTPStatus: http.StatusBadGateway, RequestSent: true,
	}, now)
	if err != nil {
		t.Fatalf("first ObserveFailure() error = %v", err)
	}
	if repo.setTempCalls != 1 {
		t.Fatalf("expected first persistence attempt, got %d", repo.setTempCalls)
	}

	repo.setTempErr = nil
	second, err := coordinator.ObserveFailure(context.Background(), account, APIKeyFailureObservation{
		AttemptID: "attempt-2", AccountID: account.ID, AccountType: account.Type,
		Platform: account.Platform, HTTPStatus: http.StatusBadGateway, RequestSent: true,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("second ObserveFailure() error = %v", err)
	}
	if repo.setTempCalls != 2 {
		t.Fatalf("pending event persistence was not retried, calls=%d", repo.setTempCalls)
	}
	if second.Generation != first.Generation || second.Streak != first.Streak {
		t.Fatalf("persistence retry advanced event: first=%+v second=%+v", first, second)
	}
}

func TestAPIKeyCooldownCoordinatorDeduplicatesAttemptID(t *testing.T) {
	now := time.Unix(15_000, 0).UTC()
	account := &Account{ID: 14, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	repo := &cooldownCoordinatorRepoStub{}
	settings := DefaultAPIKeyFailureCooldownSettings()
	settings.Policies[APIKeyFailureTransientUpstream] = APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{1, 10}, Mode: APIKeyCooldownModeHoldLast}
	coordinator := NewAPIKeyCooldownCoordinator(repo, NewMemoryAPIKeyCooldownStore(), newTestAPIKeyCooldownSettingService(t, settings))
	observation := APIKeyFailureObservation{
		AttemptID: "same-attempt", AccountID: account.ID, AccountType: account.Type,
		Platform: account.Platform, HTTPStatus: http.StatusBadGateway, RequestSent: true,
	}

	first, err := coordinator.ObserveFailure(context.Background(), account, observation, now)
	if err != nil {
		t.Fatalf("first ObserveFailure() error = %v", err)
	}
	duplicate, err := coordinator.ObserveFailure(context.Background(), account, observation, first.Until.Add(time.Second))
	if err != nil {
		t.Fatalf("duplicate ObserveFailure() error = %v", err)
	}
	if duplicate.Generation != first.Generation || duplicate.Streak != first.Streak {
		t.Fatalf("duplicate attempt advanced event: first=%+v duplicate=%+v", first, duplicate)
	}
	if repo.setTempCalls != 1 {
		t.Fatalf("duplicate attempt repeated persistence, calls=%d", repo.setTempCalls)
	}
}

func TestAPIKeyCooldownCoordinatorGuardChecksAccountWideOverload(t *testing.T) {
	now := time.Unix(16_000, 0).UTC()
	account := &Account{ID: 15, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	coordinator := NewAPIKeyCooldownCoordinator(&cooldownCoordinatorRepoStub{}, NewMemoryAPIKeyCooldownStore(), newTestAPIKeyCooldownSettingService(t, DefaultAPIKeyFailureCooldownSettings()))
	decision, err := coordinator.ObserveFailure(context.Background(), account, APIKeyFailureObservation{
		AttemptID: "overload", AccountID: account.ID, AccountType: account.Type,
		Platform: account.Platform, Model: "gpt-5", HTTPStatus: 529,
		AccountWideOverload: true, RequestSent: true,
	}, now)
	if err != nil {
		t.Fatalf("ObserveFailure() error = %v", err)
	}
	if decision.Scope != APIKeyCooldownScopeAccount {
		t.Fatalf("expected account-wide overload, got %+v", decision)
	}
	blocked, _, err := coordinator.Check(context.Background(), account, "gpt-5", now.Add(time.Second))
	if err != nil || !blocked {
		t.Fatalf("account-wide overload was not guarded: blocked=%v err=%v", blocked, err)
	}
}

type failingAPIKeyCooldownStore struct{ err error }

func (s *failingAPIKeyCooldownStore) ObserveFailure(context.Context, APIKeyCooldownKey, APIKeyCooldownPolicy, time.Time, *time.Time) (APIKeyCooldownEvent, error) {
	return APIKeyCooldownEvent{}, s.err
}
func (s *failingAPIKeyCooldownStore) Check(context.Context, APIKeyCooldownKey, time.Time) (APIKeyCooldownEvent, bool, error) {
	return APIKeyCooldownEvent{}, false, s.err
}
func (s *failingAPIKeyCooldownStore) MarkPersisted(context.Context, APIKeyCooldownKey, int64) error {
	return s.err
}
func (s *failingAPIKeyCooldownStore) ResetSuccess(context.Context, []APIKeyCooldownKey, APIKeyCooldownAttemptToken, time.Time) error {
	return s.err
}
