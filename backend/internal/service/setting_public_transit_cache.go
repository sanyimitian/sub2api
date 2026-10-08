package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

func (s *SettingService) GetPublicTransitCachePolicy(ctx context.Context) (PublicTransitCachePolicy, error) {
	defaults := DefaultPublicTransitCachePolicy()
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyPublicTransitCacheEnabled,
		SettingKeyPublicTransitCacheMinimumRate,
		SettingKeyPublicTransitCacheMaximumRate,
		SettingKeyPublicTransitCacheIncreasePercent,
		SettingKeyPublicTransitCacheGroupIncreasePercent,
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
		{SettingKeyPublicTransitCacheIncreasePercent, &policy.IncreasePercent},
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
	if raw := values[SettingKeyPublicTransitCacheGroupIncreasePercent]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &policy.GroupIncreasePercent); err != nil {
			return PublicTransitCachePolicy{}, fmt.Errorf("invalid public transit group cache increase percentages: %w", err)
		}
		if policy.GroupIncreasePercent == nil {
			policy.GroupIncreasePercent = map[int64]float64{}
		}
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
	groupIncreasePercent, err := json.Marshal(policy.GroupIncreasePercent)
	if err != nil {
		return fmt.Errorf("encode group cache increase percentages: %w", err)
	}
	return s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyPublicTransitCacheEnabled:              strconv.FormatBool(policy.Enabled),
		SettingKeyPublicTransitCacheMinimumRate:          strconv.FormatFloat(policy.MinimumRate, 'f', -1, 64),
		SettingKeyPublicTransitCacheMaximumRate:          strconv.FormatFloat(policy.MaximumRate, 'f', -1, 64),
		SettingKeyPublicTransitCacheIncreasePercent:      strconv.FormatFloat(policy.IncreasePercent, 'f', -1, 64),
		SettingKeyPublicTransitCacheGroupIncreasePercent: string(groupIncreasePercent),
		SettingKeyPublicTransitCacheLowRateMin:           strconv.FormatFloat(policy.LowRateMin, 'f', -1, 64),
		SettingKeyPublicTransitCacheLowRateMax:           strconv.FormatFloat(policy.LowRateMax, 'f', -1, 64),
	})
}
