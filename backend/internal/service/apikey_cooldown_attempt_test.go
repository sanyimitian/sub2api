package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type countingAPIKeyCooldownStore struct {
	APIKeyCooldownStore
	observeCalls int
	resetCalls   int
}

func (s *countingAPIKeyCooldownStore) ObserveFailure(ctx context.Context, key APIKeyCooldownKey, policy APIKeyCooldownPolicy, now time.Time, upstreamReset *time.Time) (APIKeyCooldownEvent, error) {
	s.observeCalls++
	return s.APIKeyCooldownStore.ObserveFailure(ctx, key, policy, now, upstreamReset)
}

func (s *countingAPIKeyCooldownStore) ResetSuccess(ctx context.Context, keys []APIKeyCooldownKey, token APIKeyCooldownAttemptToken, now time.Time) error {
	s.resetCalls++
	return s.APIKeyCooldownStore.ResetSuccess(ctx, keys, token, now)
}

func TestRateLimitServiceBeginAPIKeyCooldownAttemptBlocksActiveEvent(t *testing.T) {
	now := time.Unix(17_000, 0).UTC()
	account := &Account{ID: 31, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	store := NewMemoryAPIKeyCooldownStore()
	policy := APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{60}, Mode: APIKeyCooldownModeHoldLast}
	key := APIKeyCooldownKey{AccountID: account.ID, Family: APIKeyFailureTransientUpstream, Scope: APIKeyCooldownScopeAccount}
	_, err := store.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)
	service := newAPIKeyCooldownRateLimitServiceWithStore(account, store)

	attempt, blocked, err := service.BeginAPIKeyCooldownAttempt(context.Background(), account, "gpt-5", true, now.Add(time.Second))
	require.NoError(t, err)
	require.True(t, blocked)
	require.Nil(t, attempt)
}

func TestRateLimitServiceBeginAPIKeyCooldownAttemptCarriesTokenAndCompletesSuccessOnce(t *testing.T) {
	now := time.Unix(18_000, 0).UTC()
	account := &Account{ID: 32, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	memory := NewMemoryAPIKeyCooldownStore()
	store := &countingAPIKeyCooldownStore{APIKeyCooldownStore: memory}
	policy := APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{1}, Mode: APIKeyCooldownModeHoldLast}
	key := APIKeyCooldownKey{AccountID: account.ID, Family: APIKeyFailureTransientUpstream, Scope: APIKeyCooldownScopeAccount}
	event, err := memory.ObserveFailure(context.Background(), key, policy, now, nil)
	require.NoError(t, err)
	service := newAPIKeyCooldownRateLimitServiceWithStore(account, store)

	attempt, blocked, err := service.BeginAPIKeyCooldownAttempt(context.Background(), account, " GPT-5 ", true, event.Until.Add(time.Second))
	require.NoError(t, err)
	require.False(t, blocked)
	require.NotNil(t, attempt)
	require.NotEmpty(t, attempt.ID)
	require.Equal(t, account.ID, attempt.AccountID)
	require.Equal(t, "gpt-5", attempt.Model)
	require.True(t, attempt.ReplaySafe)
	require.Equal(t, event.Generation, attempt.Token.Generations[key.RedisKey()])

	attempt.MarkRequestSent()
	attempt.MarkResponseStarted()
	require.True(t, attempt.RequestSent())
	require.True(t, attempt.ResponseStarted())

	ctx := ContextWithAPIKeyCooldownAttempt(context.Background(), attempt)
	require.Same(t, attempt, APIKeyCooldownAttemptFromContext(ctx))
	require.NoError(t, service.ObserveAPIKeyAttemptSuccess(ctx, account, attempt, event.Until.Add(2*time.Second)))
	require.NoError(t, service.ObserveAPIKeyAttemptSuccess(ctx, account, attempt, event.Until.Add(3*time.Second)))
	require.Equal(t, 1, store.resetCalls, "one upstream attempt must report one terminal result")
}

func TestBuildAPIKeyHTTPFailureObservationUsesAttemptLifecycle(t *testing.T) {
	now := time.Unix(19_000, 0).UTC()
	account := &Account{ID: 33, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	clientCtx, cancelClient := context.WithCancel(context.Background())
	service := newAPIKeyCooldownRateLimitServiceWithStore(account, NewMemoryAPIKeyCooldownStore())
	attempt, blocked, err := service.BeginAPIKeyCooldownAttempt(clientCtx, account, " GPT-5 ", false, now)
	require.NoError(t, err)
	require.False(t, blocked)
	require.NotNil(t, attempt)
	attempt.MarkRequestSent()
	attempt.MarkResponseStarted()
	cancelClient()

	ctx := ContextWithAPIKeyCooldownAttempt(context.Background(), attempt)
	observation := buildAPIKeyHTTPFailureObservation(ctx, account, http.StatusBadGateway, nil, []byte(`{"error":{"message":"failed"}}`), "gpt-5", now.Add(time.Second))
	require.Equal(t, attempt.ID, observation.AttemptID)
	require.Equal(t, attempt.StartedAt, observation.AttemptStarted)
	require.Equal(t, attempt.Token.Generations, observation.AttemptToken.Generations)
	require.True(t, observation.RequestSent)
	require.False(t, observation.ReplaySafe)
	require.True(t, observation.ClientCanceled)
	require.False(t, observation.ClientTimedOut)
	require.True(t, observation.ResponseStarted)

	typedObservation := buildAPIKeyHTTPFailureObservation(
		context.Background(), account, http.StatusBadRequest, nil,
		[]byte(`{"error":{"type":"invalid_request_error","message":"bad request"}}`), "gpt-5", now,
	)
	require.Equal(t, "invalid_request_error", typedObservation.ErrorType)
}

func TestRateLimitServiceHandleUpstreamErrorObservesAttemptFailureOnce(t *testing.T) {
	now := time.Unix(19_500, 0).UTC()
	account := &Account{ID: 34, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	store := &countingAPIKeyCooldownStore{APIKeyCooldownStore: NewMemoryAPIKeyCooldownStore()}
	service := newAPIKeyCooldownRateLimitServiceWithStore(account, store)
	attempt, blocked, err := service.BeginAPIKeyCooldownAttempt(context.Background(), account, "gpt-5", true, now)
	require.NoError(t, err)
	require.False(t, blocked)
	attempt.MarkRequestSent()
	ctx := ContextWithAPIKeyCooldownAttempt(context.Background(), attempt)

	require.True(t, service.HandleUpstreamError(ctx, account, http.StatusBadGateway, nil, []byte(`{"error":{"message":"failed"}}`), "gpt-5"))
	require.True(t, service.HandleUpstreamError(ctx, account, http.StatusBadGateway, nil, []byte(`{"error":{"message":"failed"}}`), "gpt-5"))
	require.Equal(t, 1, store.observeCalls, "one upstream attempt must enter the failure state machine once")
}

func TestRateLimitServiceFailureTerminalPreventsLateSuccessReset(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{ID: 35, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	store := &countingAPIKeyCooldownStore{APIKeyCooldownStore: NewMemoryAPIKeyCooldownStore()}
	service := newAPIKeyCooldownRateLimitServiceWithStore(account, store)
	attempt, blocked, err := service.BeginAPIKeyCooldownAttempt(context.Background(), account, "gpt-5", true, now)
	require.NoError(t, err)
	require.False(t, blocked)
	attempt.MarkRequestSent()
	ctx := ContextWithAPIKeyCooldownAttempt(context.Background(), attempt)

	require.True(t, service.HandleUpstreamError(ctx, account, http.StatusBadGateway, nil, []byte(`{"error":{"message":"failed"}}`), "gpt-5"))
	require.NoError(t, service.ObserveAPIKeyAttemptSuccess(ctx, account, attempt, now.Add(time.Hour)))
	require.Equal(t, 0, store.resetCalls, "a late success exit must not replace an already observed failure terminal")
}

func TestAPIKeyCooldownAttemptDistinguishesClientDeadlineFromUpstreamDeadline(t *testing.T) {
	clientCtx, cancelClient := context.WithCancel(context.Background())
	defer cancelClient()
	upstreamCtx, cancelUpstream := context.WithDeadline(clientCtx, time.Now().Add(-time.Second))
	defer cancelUpstream()
	attempt := &APIKeyCooldownAttempt{clientContext: clientCtx}

	require.ErrorIs(t, upstreamCtx.Err(), context.DeadlineExceeded)
	require.False(t, attempt.ClientCanceled())
	require.False(t, attempt.ClientTimedOut(), "an upstream child deadline must not be treated as the client deadline")

	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	attempt = &APIKeyCooldownAttempt{clientContext: deadlineCtx}
	require.False(t, attempt.ClientCanceled())
	require.True(t, attempt.ClientTimedOut())
}

func TestRateLimitServiceObserveAPIKeyAttemptErrorSeparatesClientCancelAndUpstreamTimeout(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{ID: 36, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}

	t.Run("client canceled", func(t *testing.T) {
		store := &countingAPIKeyCooldownStore{APIKeyCooldownStore: NewMemoryAPIKeyCooldownStore()}
		service := newAPIKeyCooldownRateLimitServiceWithStore(account, store)
		clientCtx, cancelClient := context.WithCancel(context.Background())
		attempt, blocked, err := service.BeginAPIKeyCooldownAttempt(clientCtx, account, "gpt-5", true, now)
		require.NoError(t, err)
		require.False(t, blocked)
		attempt.MarkRequestSent()
		cancelClient()

		decision, err := service.ObserveAPIKeyAttemptError(ContextWithAPIKeyCooldownAttempt(context.Background(), attempt), account, attempt, context.Canceled, now.Add(time.Second))
		require.NoError(t, err)
		require.Equal(t, APIKeyCooldownDispositionIgnored, decision.Disposition)
		require.Equal(t, 0, store.observeCalls)
	})

	t.Run("upstream dial timeout", func(t *testing.T) {
		store := &countingAPIKeyCooldownStore{APIKeyCooldownStore: NewMemoryAPIKeyCooldownStore()}
		service := newAPIKeyCooldownRateLimitServiceWithStore(account, store)
		clientCtx := context.Background()
		attempt, blocked, err := service.BeginAPIKeyCooldownAttempt(clientCtx, account, "gpt-5", true, now)
		require.NoError(t, err)
		require.False(t, blocked)
		attempt.MarkRequestSent()
		upstreamErr := &net.OpError{Op: "dial", Net: "tcp", Err: timeoutTestError{}}

		decision, err := service.ObserveAPIKeyAttemptError(ContextWithAPIKeyCooldownAttempt(context.Background(), attempt), account, attempt, upstreamErr, now.Add(time.Second))
		require.NoError(t, err)
		require.True(t, decision.ShouldCooldown())
		require.Equal(t, APIKeyFailureTransientUpstream, decision.Family)
		require.Equal(t, 1, store.observeCalls)
	})
}

func TestRateLimitServiceObserveAPIKeyAttemptErrorPreservesFailoverHTTPDetails(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	account := &Account{ID: 37, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	store := &countingAPIKeyCooldownStore{APIKeyCooldownStore: NewMemoryAPIKeyCooldownStore()}
	service := newAPIKeyCooldownRateLimitServiceWithStore(account, store)
	attempt, blocked, err := service.BeginAPIKeyCooldownAttempt(context.Background(), account, "gpt-5", true, now)
	require.NoError(t, err)
	require.False(t, blocked)
	attempt.MarkRequestSent()
	resetAt := now.Add(2 * time.Minute)
	failoverErr := &UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{"120"}},
		ResponseBody:    []byte(`{"error":{"message":"rate limited"}}`),
	}

	decision, err := service.ObserveAPIKeyAttemptError(ContextWithAPIKeyCooldownAttempt(context.Background(), attempt), account, attempt, failoverErr, now)
	require.NoError(t, err)
	require.Equal(t, APIKeyFailureRateLimit, decision.Family)
	require.WithinDuration(t, resetAt, decision.Until, time.Second)
}

func TestRateLimitServiceObserveAPIKeyAttemptErrorExtractsTypedUpstreamErrors(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	account := &Account{ID: 38, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}

	tests := []struct {
		name        string
		upstreamErr error
		wantFamily  APIKeyFailureFamily
		wantIgnored bool
	}{
		{
			name: "images invalid request",
			upstreamErr: &OpenAIImagesUpstreamError{
				StatusCode: http.StatusBadRequest, ErrorType: "invalid_request_error",
				Code: "invalid_value", Message: "bad image request",
			},
			wantIgnored: true,
		},
		{
			name: "images service unavailable",
			upstreamErr: &OpenAIImagesUpstreamError{
				StatusCode: http.StatusServiceUnavailable, ErrorType: "server_error",
				Code: "service_unavailable", Message: "temporarily unavailable",
			},
			wantFamily: APIKeyFailureTransientUpstream,
		},
		{
			name: "grok realtime rate limit",
			upstreamErr: &GrokRealtimeDialError{
				StatusCode: http.StatusTooManyRequests,
				Err:        errors.New("websocket handshake rejected"),
			},
			wantFamily: APIKeyFailureRateLimit,
		},
		{
			name: "failover error type",
			upstreamErr: &UpstreamFailoverError{
				StatusCode:   http.StatusBadRequest,
				ResponseBody: []byte(`{"error":{"type":"invalid_request_error","code":"invalid_value","message":"bad request"}}`),
			},
			wantIgnored: true,
		},
		{
			name: "nested failover error type",
			upstreamErr: &UpstreamFailoverError{
				StatusCode:   http.StatusBadRequest,
				ResponseBody: []byte(`{"error":{"message":"{\"error\":{\"type\":\"invalid_request_error\",\"code\":\"invalid_value\"}}"}}`),
			},
			wantIgnored: true,
		},
		{
			name: "grok realtime invalid request body",
			upstreamErr: &GrokRealtimeDialError{
				StatusCode:      http.StatusBadRequest,
				ResponseHeaders: http.Header{"X-Request-Id": []string{"req_123"}},
				ResponseBody:    []byte(`{"error":{"type":"invalid_request_error","message":"bad realtime request"}}`),
				Err:             errors.New("websocket handshake rejected"),
			},
			wantIgnored: true,
		},
		{
			name: "codex models invalid request body",
			upstreamErr: &codexModelsManifestUpstreamError{
				err:        infraerrors.New(http.StatusBadGateway, "OPENAI_CODEX_MODELS_UPSTREAM_FAILED", "models upstream failed"),
				statusCode: http.StatusBadRequest,
				body:       []byte(`{"error":{"type":"invalid_request_error","message":"bad models request"}}`),
			},
			wantIgnored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &countingAPIKeyCooldownStore{APIKeyCooldownStore: NewMemoryAPIKeyCooldownStore()}
			svc := newAPIKeyCooldownRateLimitServiceWithStore(account, store)
			attempt, blocked, err := svc.BeginAPIKeyCooldownAttempt(context.Background(), account, "gpt-5", true, now)
			require.NoError(t, err)
			require.False(t, blocked)
			attempt.MarkRequestSent()

			decision, err := svc.ObserveAPIKeyAttemptError(ContextWithAPIKeyCooldownAttempt(context.Background(), attempt), account, attempt, tt.upstreamErr, now)
			require.NoError(t, err)
			if tt.wantIgnored {
				require.Equal(t, APIKeyCooldownDispositionIgnored, decision.Disposition)
				require.Equal(t, 0, store.observeCalls)
				return
			}
			require.Equal(t, tt.wantFamily, decision.Family)
			require.Equal(t, 1, store.observeCalls)
		})
	}
}

func TestRateLimitServiceObserveAPIKeyAttemptErrorExcludesLocalAndRequestScopedFailures(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{ID: 39, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "local application validation",
			err:  infraerrors.BadRequest("INVALID_REQUEST", "request construction failed"),
		},
		{
			name: "request scoped capacity",
			err: &UpstreamFailoverError{
				StatusCode:             http.StatusServiceUnavailable,
				RequestScopedTransient: true,
				ResponseBody:           []byte(`{"error":{"type":"server_error","message":"request capacity unavailable"}}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &countingAPIKeyCooldownStore{APIKeyCooldownStore: NewMemoryAPIKeyCooldownStore()}
			svc := newAPIKeyCooldownRateLimitServiceWithStore(account, store)
			attempt, blocked, err := svc.BeginAPIKeyCooldownAttempt(context.Background(), account, "gpt-5", true, now)
			require.NoError(t, err)
			require.False(t, blocked)
			attempt.MarkRequestSent()

			decision, err := svc.ObserveAPIKeyAttemptError(ContextWithAPIKeyCooldownAttempt(context.Background(), attempt), account, attempt, tt.err, now)
			require.NoError(t, err)
			require.Equal(t, APIKeyCooldownDispositionIgnored, decision.Disposition)
			require.Equal(t, 0, store.observeCalls)
		})
	}
}

type timeoutTestError struct{}

func (timeoutTestError) Error() string   { return "i/o timeout" }
func (timeoutTestError) Timeout() bool   { return true }
func (timeoutTestError) Temporary() bool { return true }

var _ net.Error = timeoutTestError{}
var _ = errors.Is

func newAPIKeyCooldownRateLimitServiceWithStore(account *Account, store APIKeyCooldownStore) *RateLimitService {
	repo := &cooldownCoordinatorRepoStub{account: account}
	coordinator := NewAPIKeyCooldownCoordinator(repo, store, staticAPIKeyCooldownSettingsProvider{settings: DefaultAPIKeyFailureCooldownSettings()})
	service := &RateLimitService{}
	service.SetAPIKeyCooldownCoordinator(coordinator)
	return service
}
