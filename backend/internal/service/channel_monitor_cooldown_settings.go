package service

import (
	"context"
	"encoding/json"
	"fmt"
)

// ChannelMonitorCooldownSettingsVersion is the version of the persisted
// channel-monitor cooldown policy.
const ChannelMonitorCooldownSettingsVersion = 1

// SettingKeyChannelMonitorCooldownSettings stores the versioned JSON policy.
const SettingKeyChannelMonitorCooldownSettings = "channel_monitor_cooldown_settings"

// ChannelMonitorCooldownSettings controls account cooldown and slow-response
// priority behavior for probes that use the current service.
type ChannelMonitorCooldownSettings struct {
	Version                      int   `json:"version"`
	CooldownMinutes              []int `json:"cooldown_minutes"`
	SlowResponseThresholdSeconds int   `json:"slow_response_threshold_seconds"`
	PriorityIncrement            int   `json:"priority_increment"`
	MaxPriorityIncrease          int   `json:"max_priority_increase"`
	PriorityAutoRecoverySeconds  int   `json:"priority_auto_recovery_seconds"`
}

const (
	channelMonitorCooldownLadderLen = 5
	channelMonitorCooldownMaxMinute = 7 * 24 * 60
	channelMonitorSlowThresholdMin  = 1
	channelMonitorSlowThresholdMax  = 24 * 60 * 60
	channelMonitorPriorityIncMin    = 1
	channelMonitorPriorityIncMax    = 100
	channelMonitorPriorityCountMin  = 1
	channelMonitorPriorityCountMax  = 100
	channelMonitorRecoveryMin       = 60
	channelMonitorRecoveryMax       = 30 * 24 * 60 * 60
)

// DefaultChannelMonitorCooldownSettings returns a fresh copy of the built-in
// policy on every call.
func DefaultChannelMonitorCooldownSettings() *ChannelMonitorCooldownSettings {
	return &ChannelMonitorCooldownSettings{
		Version:                      ChannelMonitorCooldownSettingsVersion,
		CooldownMinutes:              []int{2, 5, 30, 60, 120},
		SlowResponseThresholdSeconds: 12,
		PriorityIncrement:            1,
		MaxPriorityIncrease:          3,
		PriorityAutoRecoverySeconds:  3600,
	}
}

// NormalizeChannelMonitorCooldownSettings validates and clones a policy so
// callers cannot mutate the runtime cache through a shared slice.
func NormalizeChannelMonitorCooldownSettings(settings *ChannelMonitorCooldownSettings) (*ChannelMonitorCooldownSettings, error) {
	if settings == nil {
		return nil, fmt.Errorf("settings cannot be nil")
	}
	if settings.Version != ChannelMonitorCooldownSettingsVersion {
		return nil, fmt.Errorf("unsupported settings version %d", settings.Version)
	}
	if len(settings.CooldownMinutes) != channelMonitorCooldownLadderLen {
		return nil, fmt.Errorf("cooldown_minutes must contain exactly %d values", channelMonitorCooldownLadderLen)
	}
	normalized := &ChannelMonitorCooldownSettings{
		Version:                      ChannelMonitorCooldownSettingsVersion,
		CooldownMinutes:              append([]int(nil), settings.CooldownMinutes...),
		SlowResponseThresholdSeconds: settings.SlowResponseThresholdSeconds,
		PriorityIncrement:            settings.PriorityIncrement,
		MaxPriorityIncrease:          settings.MaxPriorityIncrease,
		PriorityAutoRecoverySeconds:  settings.PriorityAutoRecoverySeconds,
	}
	for i, minutes := range normalized.CooldownMinutes {
		if minutes <= 0 || minutes > channelMonitorCooldownMaxMinute {
			return nil, fmt.Errorf("cooldown_minutes[%d] must be between 1 and %d", i, channelMonitorCooldownMaxMinute)
		}
		if i > 0 && minutes <= normalized.CooldownMinutes[i-1] {
			return nil, fmt.Errorf("cooldown_minutes must be strictly increasing")
		}
	}
	if normalized.SlowResponseThresholdSeconds < channelMonitorSlowThresholdMin || normalized.SlowResponseThresholdSeconds > channelMonitorSlowThresholdMax {
		return nil, fmt.Errorf("slow_response_threshold_seconds must be between %d and %d", channelMonitorSlowThresholdMin, channelMonitorSlowThresholdMax)
	}
	if normalized.PriorityIncrement < channelMonitorPriorityIncMin || normalized.PriorityIncrement > channelMonitorPriorityIncMax {
		return nil, fmt.Errorf("priority_increment must be between %d and %d", channelMonitorPriorityIncMin, channelMonitorPriorityIncMax)
	}
	if normalized.MaxPriorityIncrease < channelMonitorPriorityCountMin || normalized.MaxPriorityIncrease > channelMonitorPriorityCountMax {
		return nil, fmt.Errorf("max_priority_increase must be between %d and %d", channelMonitorPriorityCountMin, channelMonitorPriorityCountMax)
	}
	if normalized.PriorityAutoRecoverySeconds < channelMonitorRecoveryMin || normalized.PriorityAutoRecoverySeconds > channelMonitorRecoveryMax {
		return nil, fmt.Errorf("priority_auto_recovery_seconds must be between %d and %d", channelMonitorRecoveryMin, channelMonitorRecoveryMax)
	}
	return normalized, nil
}

func cloneChannelMonitorCooldownSettings(settings *ChannelMonitorCooldownSettings) *ChannelMonitorCooldownSettings {
	if settings == nil {
		return nil
	}
	clone := *settings
	clone.CooldownMinutes = append([]int(nil), settings.CooldownMinutes...)
	return &clone
}

// GetChannelMonitorCooldownSettings returns the latest valid policy. Storage
// errors fail over to the last valid in-process value, then to built-in
// defaults, so a temporary database outage cannot change runtime behavior.
func (s *SettingService) GetChannelMonitorCooldownSettings(ctx context.Context) (*ChannelMonitorCooldownSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultChannelMonitorCooldownSettings(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyChannelMonitorCooldownSettings)
	if err != nil || raw == "" {
		return s.lastChannelMonitorCooldownSettings(), nil
	}
	var decoded ChannelMonitorCooldownSettings
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return s.lastChannelMonitorCooldownSettings(), nil
	}
	normalized, err := NormalizeChannelMonitorCooldownSettings(&decoded)
	if err != nil {
		return s.lastChannelMonitorCooldownSettings(), nil
	}
	s.storeChannelMonitorCooldownSettings(normalized)
	return cloneChannelMonitorCooldownSettings(normalized), nil
}

// SetChannelMonitorCooldownSettings validates, persists and immediately
// publishes a policy for all subsequent probes.
func (s *SettingService) SetChannelMonitorCooldownSettings(ctx context.Context, settings *ChannelMonitorCooldownSettings) (*ChannelMonitorCooldownSettings, error) {
	normalized, err := NormalizeChannelMonitorCooldownSettings(settings)
	if err != nil {
		return nil, err
	}
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("setting repository is unavailable")
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal channel monitor cooldown settings: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyChannelMonitorCooldownSettings, string(raw)); err != nil {
		return nil, fmt.Errorf("save channel monitor cooldown settings: %w", err)
	}
	s.storeChannelMonitorCooldownSettings(normalized)
	return cloneChannelMonitorCooldownSettings(normalized), nil
}

// ResetChannelMonitorCooldownSettings persists and returns the built-in policy.
func (s *SettingService) ResetChannelMonitorCooldownSettings(ctx context.Context) (*ChannelMonitorCooldownSettings, error) {
	return s.SetChannelMonitorCooldownSettings(ctx, DefaultChannelMonitorCooldownSettings())
}

func (s *SettingService) lastChannelMonitorCooldownSettings() *ChannelMonitorCooldownSettings {
	if s != nil {
		if cached := s.channelMonitorCooldownCache.Load(); cached != nil {
			if settings, ok := cached.(*ChannelMonitorCooldownSettings); ok {
				return cloneChannelMonitorCooldownSettings(settings)
			}
		}
	}
	return DefaultChannelMonitorCooldownSettings()
}

func (s *SettingService) storeChannelMonitorCooldownSettings(settings *ChannelMonitorCooldownSettings) {
	if s != nil && settings != nil {
		s.channelMonitorCooldownCache.Store(cloneChannelMonitorCooldownSettings(settings))
	}
}
