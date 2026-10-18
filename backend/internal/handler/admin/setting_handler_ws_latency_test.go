//go:build unit

package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsWSLatencyPartialSavePreservesThresholds(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyOpenAIWSLatencySwitchEnabled:     "false",
		service.SettingKeyOpenAIWSLatencySwitchConsecutive: "5",
		service.SettingKeyOpenAIWSLatencySwitchSlowMS:      "20000",
		service.SettingKeyOpenAIWSLatencySwitchProbeCount:  "8",
	})
	rec := doUpdateSettings(t, h, map[string]any{"openai_ws_latency_switch_enabled": true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[service.SettingKeyOpenAIWSLatencySwitchEnabled])
	require.Equal(t, "5", repo.values[service.SettingKeyOpenAIWSLatencySwitchConsecutive])
	require.Equal(t, "20000", repo.values[service.SettingKeyOpenAIWSLatencySwitchSlowMS])
	require.Equal(t, "8", repo.values[service.SettingKeyOpenAIWSLatencySwitchProbeCount])
	require.Contains(t, rec.Body.String(), `"openai_ws_latency_switch_consecutive":5`)
	require.Contains(t, rec.Body.String(), `"openai_ws_latency_switch_probe_count":8`)
}

func TestUpdateSettingsWSLatencyProbeCountPersists(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, nil)
	for _, count := range []int{2, 20, 4} {
		rec := doUpdateSettings(t, h, map[string]any{"openai_ws_latency_switch_probe_count": count}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		settings, err := h.settingService.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.Equal(t, count, settings.OpenAIWSLatencySwitchProbeCount)
		require.NotEmpty(t, repo.values[service.SettingKeyOpenAIWSLatencySwitchProbeCount])
	}
}

func TestUpdateSettingsWSLatencyRejectsExplicitInvalidNumbers(t *testing.T) {
	for _, fields := range []map[string]any{
		{"openai_ws_latency_switch_consecutive": 0},
		{"openai_ws_latency_switch_consecutive": 21},
		{"openai_ws_latency_switch_slow_ms": 999},
		{"openai_ws_latency_switch_slow_ms": 300001},
		{"openai_ws_latency_switch_probe_count": 0},
		{"openai_ws_latency_switch_probe_count": 1},
		{"openai_ws_latency_switch_probe_count": 21},
	} {
		h, _ := newStepUpSwitchTestHandler(t, nil)
		rec := doUpdateSettings(t, h, fields, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}
