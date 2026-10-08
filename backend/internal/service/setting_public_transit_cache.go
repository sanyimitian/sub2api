package service

import (
	"context"
	"fmt"
	"strconv"
)

func (s *SettingService) GetPublicTransitCachePolicy(ctx context.Context) (PublicTransitCachePolicy, error) {
	defaults := DefaultPublicTransitCachePolicy()
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyPublicTransitCacheEnabled,
		SettingKeyPublicTransitCacheMinimumRate,
		SettingKeyPublicTransitCacheMaximumRate,
		SettingKeyPublicTransitCacheLowRateMin,
		SettingKeyPublicTransitCacheLowRateMax,
	})
	if err != nil {
		return PublicTransitCachePolicy{}, err
	}
	policy := defaults
	if enabled, ok := values[SettingKeyPublicTransitCacheEnabled]; ok && enabled != "" {
		parsed, err := strconv.ParseBool(enabled)
		if err != nil {
			return PublicTransitCachePolicy{}, fmt.Errorf("invalid public transit cache policy enabled value: %w", err)
		}
		policy.Enabled = parsed
	}
	for _, item := range []struct {
		key   string
		value *float64
	}{
		{SettingKeyPublicTransitCacheMinimumRate, &policy.MinimumRate},
		{SettingKeyPublicTransitCacheMaximumRate, &policy.MaximumRate},
		{SettingKeyPublicTransitCacheLowRateMin, &policy.LowRateMin},
		{SettingKeyPublicTransitCacheLowRateMax, &policy.LowRateMax},
	} {
		raw, ok := values[item.key]
		if !ok || raw == "" {
			continue
		}
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return PublicTransitCachePolicy{}, fmt.Errorf("invalid public transit cache policy value %s: %w", item.key, err)
		}
		*item.value = parsed
	}
	if err := policy.Validate(); err != nil {
		return PublicTransitCachePolicy{}, err
	}
	return policy, nil
}

func (s *SettingService) SetPublicTransitCachePolicy(ctx context.Context, policy PublicTransitCachePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	return s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyPublicTransitCacheEnabled:     strconv.FormatBool(policy.Enabled),
		SettingKeyPublicTransitCacheMinimumRate: strconv.FormatFloat(policy.MinimumRate, 'f', -1, 64),
		SettingKeyPublicTransitCacheMaximumRate: strconv.FormatFloat(policy.MaximumRate, 'f', -1, 64),
		SettingKeyPublicTransitCacheLowRateMin:  strconv.FormatFloat(policy.LowRateMin, 'f', -1, 64),
		SettingKeyPublicTransitCacheLowRateMax:  strconv.FormatFloat(policy.LowRateMax, 'f', -1, 64),
	})
}
