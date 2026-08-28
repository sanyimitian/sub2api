package service

import "time"

func ResolveAPIKeyCooldownPolicy(settings *APIKeyFailureCooldownSettings, family APIKeyFailureFamily) (APIKeyCooldownPolicy, bool) {
	normalized, err := normalizeAPIKeyFailureCooldownSettings(settings)
	if err != nil {
		normalized = DefaultAPIKeyFailureCooldownSettings()
	}
	policy, ok := normalized.Policies[family]
	if !ok {
		policy = normalized.Policies[APIKeyFailureUnknown]
	}
	policy.Cooldowns = append([]int(nil), policy.Cooldowns...)
	return policy, policy.Enabled
}

func CalculateAPIKeyCooldownDuration(
	settings *APIKeyFailureCooldownSettings,
	family APIKeyFailureFamily,
	streak int64,
	now time.Time,
	upstreamReset *time.Time,
) time.Duration {
	policy, enabled := ResolveAPIKeyCooldownPolicy(settings, family)
	if !enabled || len(policy.Cooldowns) == 0 {
		return 0
	}
	if family == APIKeyFailureRateLimit && upstreamReset != nil && upstreamReset.After(now) {
		return upstreamReset.Sub(now)
	}
	if streak < 1 {
		streak = 1
	}

	index := streak - 1
	if policy.Mode == APIKeyCooldownModeCycle {
		index %= int64(len(policy.Cooldowns))
	} else if index >= int64(len(policy.Cooldowns)) {
		index = int64(len(policy.Cooldowns) - 1)
	}
	return time.Duration(policy.Cooldowns[index]) * time.Second
}
