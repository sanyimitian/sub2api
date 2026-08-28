package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type apiKeyCooldownRateLimitRepoStub struct {
	AccountRepository
	tempCalls  int
	modelCalls int
}

func (r *apiKeyCooldownRateLimitRepoStub) SetError(context.Context, int64, string) error { return nil }

func (r *apiKeyCooldownRateLimitRepoStub) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

func (r *apiKeyCooldownRateLimitRepoStub) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	r.modelCalls++
	return nil
}

func newAPIKeyCooldownRateLimitService(repo AccountRepository) *RateLimitService {
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	coordinator := NewAPIKeyCooldownCoordinator(repo, NewMemoryAPIKeyCooldownStore(), staticAPIKeyCooldownSettingsProvider{settings: DefaultAPIKeyFailureCooldownSettings()})
	svc.SetAPIKeyCooldownCoordinator(coordinator)
	return svc
}

func TestRateLimitServiceHandleUpstreamError_APIKeyNonPoolUsesCooldownCoordinator(t *testing.T) {
	repo := &apiKeyCooldownRateLimitRepoStub{}
	svc := newAPIKeyCooldownRateLimitService(repo)
	account := &Account{ID: 101, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"upstream unavailable"}}`), "gpt-5")
	if !shouldDisable {
		t.Fatal("a newly cooled API key account must be excluded from the current request")
	}
	if repo.tempCalls != 1 {
		t.Fatalf("expected one account cooldown persistence, got %d", repo.tempCalls)
	}
}

func TestRateLimitServiceHandleUpstreamError_APIKeyPoolSkipsCooldownCoordinator(t *testing.T) {
	repo := &apiKeyCooldownRateLimitRepoStub{}
	svc := newAPIKeyCooldownRateLimitService(repo)
	account := &Account{ID: 102, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"pool_mode": true}}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"upstream unavailable"}}`), "gpt-5")
	if shouldDisable {
		t.Fatal("pool mode must retain its existing handling")
	}
	if repo.tempCalls != 0 || repo.modelCalls != 0 {
		t.Fatalf("pool mode unexpectedly persisted cooldown: temp=%d model=%d", repo.tempCalls, repo.modelCalls)
	}
}

func TestRateLimitServiceHandleUpstreamError_APIKeyCustomRuleHitDoesNotDoubleCount(t *testing.T) {
	repo := &apiKeyCooldownRateLimitRepoStub{}
	svc := newAPIKeyCooldownRateLimitService(repo)
	account := &Account{ID: 103, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusServiceUnavailable)},
		}}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"upstream unavailable"}}`), "gpt-5")
	if !shouldDisable {
		t.Fatal("a matched custom error code must retain existing exclusion behavior")
	}
	if repo.tempCalls != 0 || repo.modelCalls != 0 {
		t.Fatalf("custom rule hit unexpectedly persisted unified cooldown: temp=%d model=%d", repo.tempCalls, repo.modelCalls)
	}
}

func TestRateLimitServiceHandleUpstreamError_APIKeyCustomRuleMissUsesDefaultCooldown(t *testing.T) {
	repo := &apiKeyCooldownRateLimitRepoStub{}
	svc := newAPIKeyCooldownRateLimitService(repo)
	account := &Account{ID: 104, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(http.StatusTooManyRequests)},
		}}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"upstream unavailable"}}`), "gpt-5")
	if !shouldDisable {
		t.Fatal("an unrecognized custom error code must continue through default cooldown")
	}
	if repo.tempCalls != 1 {
		t.Fatalf("expected default cooldown persistence after custom-rule miss, got %d", repo.tempCalls)
	}
}
