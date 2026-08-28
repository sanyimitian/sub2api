package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

func (s *SettingService) GetAPIKeyFailureCooldownSettings(ctx context.Context) (*APIKeyFailureCooldownSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultAPIKeyFailureCooldownSettings(), nil
	}

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAPIKeyFailureCooldownSettings)
	if err != nil {
		return s.lastValidAPIKeyFailureCooldownSettings(), nil
	}
	if raw == "" {
		defaults := DefaultAPIKeyFailureCooldownSettings()
		s.storeAPIKeyFailureCooldownSettings(defaults)
		return cloneAPIKeyFailureCooldownSettings(defaults), nil
	}

	var decoded APIKeyFailureCooldownSettings
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return s.lastValidAPIKeyFailureCooldownSettings(), nil
	}
	normalized, err := normalizeAPIKeyFailureCooldownSettings(&decoded)
	if err != nil {
		return s.lastValidAPIKeyFailureCooldownSettings(), nil
	}
	s.storeAPIKeyFailureCooldownSettings(normalized)
	return cloneAPIKeyFailureCooldownSettings(normalized), nil
}

func (s *SettingService) SetAPIKeyFailureCooldownSettings(ctx context.Context, settings *APIKeyFailureCooldownSettings) (*APIKeyFailureCooldownSettings, error) {
	normalized, err := normalizeAPIKeyFailureCooldownSettings(settings)
	if err != nil {
		return nil, err
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("setting repository is unavailable")
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal api key failure cooldown settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAPIKeyFailureCooldownSettings, string(raw)); err != nil {
		return nil, fmt.Errorf("save api key failure cooldown settings: %w", err)
	}
	s.storeAPIKeyFailureCooldownSettings(normalized)
	return cloneAPIKeyFailureCooldownSettings(normalized), nil
}

func normalizeAPIKeyFailureCooldownSettings(settings *APIKeyFailureCooldownSettings) (*APIKeyFailureCooldownSettings, error) {
	if settings == nil {
		return nil, fmt.Errorf("settings cannot be nil")
	}
	if settings.Version != APIKeyFailureCooldownSettingsVersion {
		return nil, fmt.Errorf("unsupported settings version %d", settings.Version)
	}
	if len(settings.Policies) != len(allAPIKeyFailureFamilies) {
		return nil, fmt.Errorf("policies must contain every supported failure family exactly once")
	}

	normalized := &APIKeyFailureCooldownSettings{
		Version:  APIKeyFailureCooldownSettingsVersion,
		Policies: make(map[APIKeyFailureFamily]APIKeyCooldownPolicy, len(allAPIKeyFailureFamilies)),
	}
	for _, family := range allAPIKeyFailureFamilies {
		policy, ok := settings.Policies[family]
		if !ok {
			return nil, fmt.Errorf("missing policy for failure family %q", family)
		}
		if policy.Mode != APIKeyCooldownModeHoldLast && policy.Mode != APIKeyCooldownModeCycle {
			return nil, fmt.Errorf("unsupported cooldown mode %q for failure family %q", policy.Mode, family)
		}
		if len(policy.Cooldowns) == 0 {
			return nil, fmt.Errorf("cooldowns cannot be empty for failure family %q", family)
		}

		seen := make(map[int]struct{}, len(policy.Cooldowns))
		cooldowns := make([]int, 0, len(policy.Cooldowns))
		for _, seconds := range policy.Cooldowns {
			if seconds <= 0 {
				return nil, fmt.Errorf("cooldowns must contain positive seconds for failure family %q", family)
			}
			if _, exists := seen[seconds]; exists {
				continue
			}
			seen[seconds] = struct{}{}
			cooldowns = append(cooldowns, seconds)
		}
		sort.Ints(cooldowns)
		normalized.Policies[family] = APIKeyCooldownPolicy{
			Enabled:   policy.Enabled,
			Cooldowns: cooldowns,
			Mode:      policy.Mode,
		}
	}
	return normalized, nil
}

func cloneAPIKeyFailureCooldownSettings(settings *APIKeyFailureCooldownSettings) *APIKeyFailureCooldownSettings {
	if settings == nil {
		return nil
	}
	clone := &APIKeyFailureCooldownSettings{
		Version:  settings.Version,
		Policies: make(map[APIKeyFailureFamily]APIKeyCooldownPolicy, len(settings.Policies)),
	}
	for family, policy := range settings.Policies {
		policy.Cooldowns = append([]int(nil), policy.Cooldowns...)
		clone.Policies[family] = policy
	}
	return clone
}

func (s *SettingService) lastValidAPIKeyFailureCooldownSettings() *APIKeyFailureCooldownSettings {
	if s != nil {
		if cached := s.apiKeyFailureCooldownCache.Load(); cached != nil {
			if settings, ok := cached.(*APIKeyFailureCooldownSettings); ok {
				return cloneAPIKeyFailureCooldownSettings(settings)
			}
		}
	}
	return DefaultAPIKeyFailureCooldownSettings()
}

func (s *SettingService) storeAPIKeyFailureCooldownSettings(settings *APIKeyFailureCooldownSettings) {
	if s != nil && settings != nil {
		s.apiKeyFailureCooldownCache.Store(cloneAPIKeyFailureCooldownSettings(settings))
	}
}
