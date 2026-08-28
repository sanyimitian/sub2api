package service

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSanitizeAPIKeyErrorSummaryRedactsAndBounds(t *testing.T) {
	raw := `authorization=Bearer top-secret api_key=sk-live-secret token=xai-secret ` + strings.Repeat("错误详情", 300)
	got := SanitizeAPIKeyErrorSummary(raw)

	require.NotContains(t, got, "top-secret")
	require.NotContains(t, got, "sk-live-secret")
	require.NotContains(t, got, "xai-secret")
	require.LessOrEqual(t, utf8.RuneCountInString(got), APIKeyErrorSummaryMaxRunes)
	require.True(t, utf8.ValidString(got))
}

func TestSanitizeAPIKeyFailureObservationAllowsOnlyClassificationHeaders(t *testing.T) {
	observation := APIKeyFailureObservation{
		AccountType:  AccountTypeAPIKey,
		HTTPStatus:   429,
		ErrorCode:    " rate_limit_exceeded\nsecret ",
		ErrorType:    " rate_limit_error ",
		ErrorSummary: "access_token=secret rate limited",
		Headers: http.Header{
			"Authorization":     []string{"Bearer secret"},
			"Retry-After":       []string{"60"},
			"X-RateLimit-Reset": []string{"1787832300"},
			"X-Debug-Secret":    []string{"secret"},
		},
	}

	got := SanitizeAPIKeyFailureObservation(observation)
	require.Equal(t, "rate_limit_exceeded_secret", got.ErrorCode)
	require.Equal(t, "rate_limit_error", got.ErrorType)
	require.NotContains(t, got.ErrorSummary, "secret")
	require.Equal(t, "60", got.Headers.Get("Retry-After"))
	require.Equal(t, "1787832300", apiKeyHeaderValue(got.Headers, "X-RateLimit-Reset"))
	require.Empty(t, got.Headers.Get("Authorization"))
	require.Empty(t, got.Headers.Get("X-Debug-Secret"))
}

func TestAPIKeyCooldownDefaultPoliciesCoverEveryFamily(t *testing.T) {
	settings := DefaultAPIKeyFailureCooldownSettings()
	tests := []struct {
		family APIKeyFailureFamily
		want   []int
		mode   APIKeyCooldownMode
		scope  APIKeyCooldownScope
	}{
		{APIKeyFailureRateLimit, []int{60, 300, 900}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeAccount},
		{APIKeyFailureOverload, []int{60, 300}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeModel},
		{APIKeyFailureTransientUpstream, []int{30, 120, 600}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeAccount},
		{APIKeyFailureTemporaryForbidden, []int{300}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeAccount},
		{APIKeyFailureAccountBlocked, []int{300}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeAccount},
		{APIKeyFailureUnauthorized, []int{1800}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeAccount},
		{APIKeyFailureQuotaExhausted, []int{3600}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeAccount},
		{APIKeyFailureModelUnsupported, []int{1800}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeModel},
		{APIKeyFailureGlobalUpstream, []int{1800}, APIKeyCooldownModeHoldLast, APIKeyCooldownScopeAccount},
		{APIKeyFailureUnknown, []int{60, 600, 1800}, APIKeyCooldownModeCycle, APIKeyCooldownScopeAccount},
	}
	for _, tt := range tests {
		t.Run(string(tt.family), func(t *testing.T) {
			policy, ok := settings.Policies[tt.family]
			require.True(t, ok)
			require.True(t, policy.Enabled)
			require.Equal(t, tt.want, policy.Cooldowns)
			require.Equal(t, tt.mode, policy.Mode)
			require.Equal(t, tt.scope, tt.family.DefaultScope())
		})
	}
}
