package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCalculateAPIKeyCooldownDurationHoldLastAndCycle(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

	for streak, want := range []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute} {
		got := CalculateAPIKeyCooldownDuration(DefaultAPIKeyFailureCooldownSettings(), APIKeyFailureRateLimit, int64(streak+1), now, nil)
		require.Equal(t, want, got)
	}
	for streak, want := range []time.Duration{time.Minute, 10 * time.Minute, 30 * time.Minute, time.Minute, 10 * time.Minute} {
		got := CalculateAPIKeyCooldownDuration(DefaultAPIKeyFailureCooldownSettings(), APIKeyFailureUnknown, int64(streak+1), now, nil)
		require.Equal(t, want, got)
	}
}

func TestCalculateAPIKeyCooldownDurationOverrideFallbackAndUpstreamReset(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	settings := DefaultAPIKeyFailureCooldownSettings()
	settings.Policies[APIKeyFailureTransientUpstream] = APIKeyCooldownPolicy{
		Enabled: true, Cooldowns: []int{4, 8}, Mode: APIKeyCooldownModeCycle,
	}
	require.Equal(t, 4*time.Second, CalculateAPIKeyCooldownDuration(settings, APIKeyFailureTransientUpstream, 3, now, nil))

	invalid := cloneAPIKeyFailureCooldownSettings(settings)
	invalid.Policies[APIKeyFailureTransientUpstream] = APIKeyCooldownPolicy{Enabled: true, Mode: APIKeyCooldownModeHoldLast}
	require.Equal(t, 30*time.Second, CalculateAPIKeyCooldownDuration(invalid, APIKeyFailureTransientUpstream, 1, now, nil))
	require.Equal(t, 30*time.Second, CalculateAPIKeyCooldownDuration(nil, APIKeyFailureTransientUpstream, 1, now, nil))

	reset := now.Add(83 * time.Second)
	require.Equal(t, 83*time.Second, CalculateAPIKeyCooldownDuration(settings, APIKeyFailureRateLimit, 9, now, &reset))
	expired := now.Add(-time.Second)
	require.Equal(t, 15*time.Minute, CalculateAPIKeyCooldownDuration(settings, APIKeyFailureRateLimit, 9, now, &expired))
}

func TestResolveAPIKeyCooldownPolicyDisabled(t *testing.T) {
	settings := DefaultAPIKeyFailureCooldownSettings()
	policy := settings.Policies[APIKeyFailureUnauthorized]
	policy.Enabled = false
	settings.Policies[APIKeyFailureUnauthorized] = policy

	resolved, enabled := ResolveAPIKeyCooldownPolicy(settings, APIKeyFailureUnauthorized)
	require.False(t, enabled)
	require.Equal(t, policy, resolved)
}
