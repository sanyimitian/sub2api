//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSLatencySettingsDefaultsAndValidation(t *testing.T) {
	resetGatewayForwardingSettingsCacheForTest(t)
	repo := newMockSettingRepo()
	svc := NewSettingService(repo, &config.Config{})
	defaults := svc.parseSettings(map[string]string{})
	require.False(t, defaults.OpenAIWSLatencySwitchEnabled)
	require.Equal(t, 3, defaults.OpenAIWSLatencySwitchConsecutive)
	require.Equal(t, 15000, defaults.OpenAIWSLatencySwitchSlowMS)
	require.Equal(t, 6, defaults.OpenAIWSLatencySwitchProbeCount)
	require.Equal(t, 300, defaults.OpenAIWSLatencySwitchWaitSeconds)
	require.Equal(t, 600, defaults.OpenAIWSLatencyProbeReuseSeconds)
	defaults.OpenAIWSLatencySwitchEnabled = true
	defaults.OpenAIWSLatencySwitchConsecutive = 5
	defaults.OpenAIWSLatencySwitchSlowMS = 20000
	defaults.OpenAIWSLatencySwitchProbeCount = 8
	defaults.OpenAIWSLatencySwitchWaitSeconds = 480
	defaults.OpenAIWSLatencyProbeReuseSeconds = 720
	updates, err := svc.buildSystemSettingsUpdates(context.Background(), defaults)
	require.NoError(t, err)
	require.Equal(t, "true", updates[SettingKeyOpenAIWSLatencySwitchEnabled])
	require.Equal(t, "5", updates[SettingKeyOpenAIWSLatencySwitchConsecutive])
	require.Equal(t, "20000", updates[SettingKeyOpenAIWSLatencySwitchSlowMS])
	require.Equal(t, "8", updates[SettingKeyOpenAIWSLatencySwitchProbeCount])
	require.Equal(t, "480", updates[SettingKeyOpenAIWSLatencySwitchWaitSeconds])
	require.Equal(t, "720", updates[SettingKeyOpenAIWSLatencyProbeReuseSeconds])
	roundTrip := svc.parseSettings(updates)
	require.Equal(t, defaults.OpenAIWSLatencySwitchConsecutive, roundTrip.OpenAIWSLatencySwitchConsecutive)
	require.Equal(t, 8, roundTrip.OpenAIWSLatencySwitchProbeCount)
	require.Equal(t, 480, roundTrip.OpenAIWSLatencySwitchWaitSeconds)
	require.Equal(t, 720, roundTrip.OpenAIWSLatencyProbeReuseSeconds)
	svc.refreshCachedSettings(roundTrip)
	require.Equal(t, 8, svc.GetOpenAIWSLatencySwitchSettings(context.Background()).ProbeCount)
	require.Equal(t, 480, svc.GetOpenAIWSLatencySwitchSettings(context.Background()).WaitSeconds)
	defaults.OpenAIWSLatencySwitchConsecutive = 21
	_, err = svc.buildSystemSettingsUpdates(context.Background(), defaults)
	require.ErrorContains(t, err, SettingKeyOpenAIWSLatencySwitchConsecutive)
	defaults.OpenAIWSLatencySwitchConsecutive = 3
	defaults.OpenAIWSLatencySwitchSlowMS = 999
	_, err = svc.buildSystemSettingsUpdates(context.Background(), defaults)
	require.ErrorContains(t, err, SettingKeyOpenAIWSLatencySwitchSlowMS)
	defaults.OpenAIWSLatencySwitchSlowMS = 15000
	for _, invalid := range []int{-1, 1, 21} {
		defaults.OpenAIWSLatencySwitchProbeCount = invalid
		_, err = svc.buildSystemSettingsUpdates(context.Background(), defaults)
		require.ErrorContains(t, err, SettingKeyOpenAIWSLatencySwitchProbeCount)
	}
	defaults.OpenAIWSLatencySwitchProbeCount = 6
	for _, invalid := range []int{-1, MaxOpenAIWSLatencyProbeReuseSeconds + 1} {
		defaults.OpenAIWSLatencyProbeReuseSeconds = invalid
		_, err = svc.buildSystemSettingsUpdates(context.Background(), defaults)
		require.ErrorContains(t, err, SettingKeyOpenAIWSLatencyProbeReuseSeconds)
	}
	defaults.OpenAIWSLatencyProbeReuseSeconds = 720
	for _, invalid := range []int{-1, MaxOpenAIWSLatencySwitchWaitSeconds + 1} {
		defaults.OpenAIWSLatencySwitchWaitSeconds = invalid
		_, err = svc.buildSystemSettingsUpdates(context.Background(), defaults)
		require.ErrorContains(t, err, SettingKeyOpenAIWSLatencySwitchWaitSeconds)
	}
}
