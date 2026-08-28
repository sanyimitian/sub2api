package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyFailureObservationCarriesAttemptLifecycle(t *testing.T) {
	started := time.Now().Add(-time.Second)
	token := APIKeyCooldownAttemptToken{
		StartedAt: started,
		Generations: map[string]int64{
			"account:42:rate_limit": 7,
		},
	}
	observation := APIKeyFailureObservation{
		AttemptID:       "attempt-1",
		AttemptStarted:  started,
		AttemptToken:    token,
		AccountID:       42,
		AccountType:     AccountTypeAPIKey,
		PoolMode:        false,
		Platform:        PlatformOpenAI,
		Model:           "gpt-5",
		HTTPStatus:      http.StatusTooManyRequests,
		Headers:         http.Header{"Retry-After": []string{"60"}},
		ErrorCode:       "rate_limit_exceeded",
		ErrorType:       "rate_limit_error",
		ErrorSummary:    "rate limited",
		TransportError:  APIKeyTransportErrorNone,
		RequestSent:     true,
		ResponseStarted: false,
	}

	require.True(t, observation.Applicable())
	require.Equal(t, int64(7), observation.AttemptToken.Generations["account:42:rate_limit"])
	require.Equal(t, "60", observation.Headers.Get("Retry-After"))
}

func TestAPIKeyCooldownDecisionAndSuccessContracts(t *testing.T) {
	until := time.Now().Add(time.Minute)
	decision := APIKeyCooldownDecision{
		Disposition:  APIKeyCooldownDispositionCooldown,
		Family:       APIKeyFailureUnknown,
		Scope:        APIKeyCooldownScopeAccount,
		Streak:       1,
		Generation:   3,
		Until:        until,
		Exclude:      true,
		SafeToReplay: true,
	}
	require.True(t, decision.ShouldCooldown())

	success := APIKeyCooldownSuccess{
		AttemptID:      "attempt-1",
		AccountID:      42,
		Model:          "gpt-5",
		AttemptStarted: time.Now(),
		AttemptToken:   APIKeyCooldownAttemptToken{Generations: map[string]int64{"key": 3}},
	}
	require.Equal(t, "attempt-1", success.AttemptID)
}
