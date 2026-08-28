//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type openAIAPIKeyCooldownAccountRepo struct {
	openAIImagesFailoverAccountRepo
	mu               sync.Mutex
	tempCooldownIDs  []int64
	modelCooldownIDs []int64
	errorIDs         []int64
	tempCooldownsLog []apiKeyCooldownPersistenceRecord
}

type apiKeyCooldownPersistenceRecord struct {
	accountID int64
	until     time.Time
	reason    string
}

func (r *openAIAPIKeyCooldownAccountRepo) SetTempUnschedulable(_ context.Context, accountID int64, until time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tempCooldownIDs = append(r.tempCooldownIDs, accountID)
	r.tempCooldownsLog = append(r.tempCooldownsLog, apiKeyCooldownPersistenceRecord{accountID: accountID, until: until, reason: reason})
	return nil
}

func (r *openAIAPIKeyCooldownAccountRepo) SetModelRateLimit(_ context.Context, accountID int64, _ string, _ time.Time, _ ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modelCooldownIDs = append(r.modelCooldownIDs, accountID)
	return nil
}

func (r *openAIAPIKeyCooldownAccountRepo) tempCooldowns() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.tempCooldownIDs...)
}

func (r *openAIAPIKeyCooldownAccountRepo) SetError(_ context.Context, accountID int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errorIDs = append(r.errorIDs, accountID)
	return nil
}

func (r *openAIAPIKeyCooldownAccountRepo) errors() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.errorIDs...)
}

type scriptedAPIKeyCooldownUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
	respond    func(call int, accountID int64) (int, http.Header, string)
}

func (u *scriptedAPIKeyCooldownUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	call := len(u.accountIDs)
	u.mu.Unlock()
	status, headers, body := u.respond(call, accountID)
	if headers == nil {
		headers = http.Header{"Content-Type": []string{"application/json"}}
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(bytes.NewBufferString(body))}, nil
}

func (u *scriptedAPIKeyCooldownUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

type logicalAPIKeyCooldownEvent struct {
	now   time.Time
	event service.APIKeyCooldownEvent
}

type logicalAPIKeyCooldownStore struct {
	inner  *service.MemoryAPIKeyCooldownStore
	mu     sync.Mutex
	now    time.Time
	events []logicalAPIKeyCooldownEvent
}

func newLogicalAPIKeyCooldownStore(now time.Time) *logicalAPIKeyCooldownStore {
	return &logicalAPIKeyCooldownStore{inner: service.NewMemoryAPIKeyCooldownStore(), now: now.UTC()}
}

func (s *logicalAPIKeyCooldownStore) currentTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *logicalAPIKeyCooldownStore) ObserveFailure(ctx context.Context, key service.APIKeyCooldownKey, policy service.APIKeyCooldownPolicy, _ time.Time, upstreamReset *time.Time) (service.APIKeyCooldownEvent, error) {
	now := s.currentTime()
	event, err := s.inner.ObserveFailure(ctx, key, policy, now, upstreamReset)
	if err == nil && event.Created {
		s.mu.Lock()
		s.events = append(s.events, logicalAPIKeyCooldownEvent{now: now, event: event})
		s.mu.Unlock()
	}
	return event, err
}

func (s *logicalAPIKeyCooldownStore) Check(ctx context.Context, key service.APIKeyCooldownKey, _ time.Time) (service.APIKeyCooldownEvent, bool, error) {
	return s.inner.Check(ctx, key, s.currentTime())
}

func (s *logicalAPIKeyCooldownStore) MarkPersisted(ctx context.Context, key service.APIKeyCooldownKey, generation int64) error {
	return s.inner.MarkPersisted(ctx, key, generation)
}

func (s *logicalAPIKeyCooldownStore) ResetSuccess(ctx context.Context, keys []service.APIKeyCooldownKey, token service.APIKeyCooldownAttemptToken, _ time.Time) error {
	return s.inner.ResetSuccess(ctx, keys, token, s.currentTime())
}

func (s *logicalAPIKeyCooldownStore) advancePast(until time.Time) {
	s.mu.Lock()
	s.now = until.UTC().Add(time.Second)
	s.mu.Unlock()
}

func (s *logicalAPIKeyCooldownStore) createdEvents() []logicalAPIKeyCooldownEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]logicalAPIKeyCooldownEvent(nil), s.events...)
}

func newOpenAIAPIKeyCooldownTestHandler(
	t *testing.T,
	repo *openAIAPIKeyCooldownAccountRepo,
	store service.APIKeyCooldownStore,
	upstream service.HTTPUpstream,
) *OpenAIGatewayHandler {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	rateLimitService := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	rateLimitService.SetAPIKeyCooldownCoordinator(service.NewAPIKeyCooldownCoordinator(repo, store, nil))
	gatewayService := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		nil, rateLimitService, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(nil),
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	handler.maxAccountSwitches = 10
	return handler
}

func openAIAPIKeyCooldownTestAccount(id int64, priority int) service.Account {
	return service.Account{
		ID: id, Name: "api-key", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: priority,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://api.example.test"},
		Extra:       map[string]any{"openai_passthrough": true},
	}
}

func TestOpenAIResponsesAPIKeyCooldownBlocksSnapshotStaleAccountsOnNextRequest(t *testing.T) {
	accounts := []service.Account{
		{
			ID: 1, Name: "api-key-1", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"api_key": "sk-1", "base_url": "https://api.example.test"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
		{
			ID: 2, Name: "api-key-2", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 1,
			Credentials: map[string]any{"api_key": "sk-2", "base_url": "https://api.example.test"},
			Extra:       map[string]any{"openai_passthrough": true},
		},
	}
	repo := &openAIAPIKeyCooldownAccountRepo{
		openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts},
	}
	upstream := &openAIResponsesFailoverCancelUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false

	rateLimitService := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	rateLimitService.SetAPIKeyCooldownCoordinator(service.NewAPIKeyCooldownCoordinator(
		repo,
		service.NewMemoryAPIKeyCooldownStore(),
		nil,
	))
	gatewayService := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		nil, rateLimitService, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(nil),
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)
	handler.maxAccountSwitches = 10

	firstContext, _ := newOpenAIResponsesFailoverTestContext(t, nil)
	handler.Responses(firstContext)
	require.Equal(t, []int64{1, 2}, upstream.calls())
	require.ElementsMatch(t, []int64{1, 2}, repo.tempCooldowns())

	secondContext, _ := newOpenAIResponsesFailoverTestContext(t, nil)
	handler.Responses(secondContext)
	require.Equal(t, []int64{1, 2}, upstream.calls(), "shared guard must block stale scheduler candidates before send")
}

func TestOpenAIResponsesAPIKeyCooldownPermanentAndRequestErrorsDoNotCool(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantErrorIDs []int64
	}{
		{
			name:         "permanent invalid key uses existing account error handling",
			status:       http.StatusUnauthorized,
			body:         `{"error":{"type":"authentication_error","code":"invalid_api_key","message":"invalid api key"}}`,
			wantErrorIDs: []int64{1},
		},
		{
			name:   "invalid request is not an account health failure",
			status: http.StatusBadRequest,
			body:   `{"error":{"type":"invalid_request_error","code":"invalid_value","message":"bad input"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &openAIAPIKeyCooldownAccountRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{
				accounts: []service.Account{openAIAPIKeyCooldownTestAccount(1, 0)},
			}}
			upstream := &scriptedAPIKeyCooldownUpstream{respond: func(_ int, _ int64) (int, http.Header, string) {
				return tt.status, nil, tt.body
			}}
			handler := newOpenAIAPIKeyCooldownTestHandler(t, repo, service.NewMemoryAPIKeyCooldownStore(), upstream)

			requestContext, _ := newOpenAIResponsesFailoverTestContext(t, nil)
			handler.Responses(requestContext)

			require.Equal(t, []int64{1}, upstream.calls())
			require.Empty(t, repo.tempCooldowns(), "request and permanent credential errors must not enter temporary cooldown")
			require.Equal(t, tt.wantErrorIDs, repo.errors())
		})
	}
}

func TestOpenAIResponsesAPIKeyCooldownKnownFailureFailsOverWithinRequest(t *testing.T) {
	repo := &openAIAPIKeyCooldownAccountRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{
		accounts: []service.Account{openAIAPIKeyCooldownTestAccount(1, 0), openAIAPIKeyCooldownTestAccount(2, 1)},
	}}
	upstream := &scriptedAPIKeyCooldownUpstream{respond: func(call int, _ int64) (int, http.Header, string) {
		if call == 1 {
			return http.StatusServiceUnavailable, nil, `{"error":{"type":"server_error","message":"temporarily unavailable"}}`
		}
		return http.StatusOK, nil, `{"id":"resp_ok","object":"response","status":"completed","model":"gpt-5.1","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	}}
	handler := newOpenAIAPIKeyCooldownTestHandler(t, repo, service.NewMemoryAPIKeyCooldownStore(), upstream)

	requestContext, recorder := newOpenAIResponsesFailoverTestContext(t, nil)
	handler.Responses(requestContext)

	require.Equal(t, []int64{1, 2}, upstream.calls())
	require.Equal(t, []int64{1}, repo.tempCooldowns())
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestOpenAIResponsesAPIKeyCooldownUnknownFailureCyclesAcrossRequests(t *testing.T) {
	store := newLogicalAPIKeyCooldownStore(time.Now().UTC())
	repo := &openAIAPIKeyCooldownAccountRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{
		accounts: []service.Account{openAIAPIKeyCooldownTestAccount(1, 0)},
	}}
	upstream := &scriptedAPIKeyCooldownUpstream{respond: func(_ int, _ int64) (int, http.Header, string) {
		return 520, http.Header{"Content-Type": []string{"text/html"}}, "<html>unknown upstream failure</html>"
	}}
	wantDurations := []time.Duration{time.Minute, 10 * time.Minute, 30 * time.Minute, time.Minute}

	for index, wantDuration := range wantDurations {
		// Rebuild the handler to model a fresh service instance while retaining
		// the shared cooldown event store and database-backed account state.
		handler := newOpenAIAPIKeyCooldownTestHandler(t, repo, store, upstream)
		requestContext, _ := newOpenAIResponsesFailoverTestContext(t, nil)
		handler.Responses(requestContext)

		events := store.createdEvents()
		require.Len(t, events, index+1)
		created := events[index]
		require.Equal(t, service.APIKeyFailureUnknown, created.event.Key.Family)
		require.Equal(t, int64(index+1), created.event.Streak)
		require.Equal(t, wantDuration, created.event.Until.Sub(created.now))
		store.advancePast(created.event.Until)
	}
	require.Equal(t, []int64{1, 1, 1, 1}, upstream.calls())
}
