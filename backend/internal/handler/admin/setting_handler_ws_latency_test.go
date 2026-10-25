//go:build unit

package admin

import (
	"context"
	"fmt"
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
		service.SettingKeyOpenAIWSLatencySwitchMinTPS:      "42.5",
		service.SettingKeyOpenAIWSLatencySwitchProbeCount:  "8",
		service.SettingKeyOpenAIWSLatencySwitchWaitSeconds: "300",
	})
	rec := doUpdateSettings(t, h, map[string]any{"openai_ws_latency_switch_enabled": true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[service.SettingKeyOpenAIWSLatencySwitchEnabled])
	require.Equal(t, "5", repo.values[service.SettingKeyOpenAIWSLatencySwitchConsecutive])
	require.Equal(t, "20000", repo.values[service.SettingKeyOpenAIWSLatencySwitchSlowMS])
	require.Equal(t, "42.5", repo.values[service.SettingKeyOpenAIWSLatencySwitchMinTPS])
	require.Equal(t, "8", repo.values[service.SettingKeyOpenAIWSLatencySwitchProbeCount])
	require.Equal(t, "300", repo.values[service.SettingKeyOpenAIWSLatencySwitchWaitSeconds])
	require.Contains(t, rec.Body.String(), `"openai_ws_latency_switch_consecutive":5`)
	require.Contains(t, rec.Body.String(), `"openai_ws_latency_switch_probe_count":8`)
	require.Contains(t, rec.Body.String(), `"openai_ws_latency_switch_min_tps":42.5`)
	require.Contains(t, rec.Body.String(), `"openai_ws_latency_switch_wait_seconds":300`)
}

func TestUpdateSettingsWSLatencyWaitSecondsPersists(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, nil)
	for _, seconds := range []int{1, 300, 3600} {
		rec := doUpdateSettings(t, h, map[string]any{"openai_ws_latency_switch_wait_seconds": seconds}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		settings, err := h.settingService.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.Equal(t, seconds, settings.OpenAIWSLatencySwitchWaitSeconds)
		require.Equal(t, fmt.Sprint(seconds), repo.values[service.SettingKeyOpenAIWSLatencySwitchWaitSeconds])
	}
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

func TestUpdateSettingsWSLatencyMinTPSPersistsAndCanBeCleared(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, nil)
	for _, tps := range []float64{42.5, 0} {
		rec := doUpdateSettings(t, h, map[string]any{"openai_ws_latency_switch_min_tps": tps}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		settings, err := h.settingService.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.Equal(t, tps, settings.OpenAIWSLatencySwitchMinTPS)
		require.Equal(t, fmt.Sprint(tps), repo.values[service.SettingKeyOpenAIWSLatencySwitchMinTPS])
	}
}

func TestUpdateSettingsWSLatencyRejectsExplicitInvalidNumbers(t *testing.T) {
	for _, fields := range []map[string]any{
		{"openai_ws_latency_switch_consecutive": 0},
		{"openai_ws_latency_switch_consecutive": 21},
		{"openai_ws_latency_switch_slow_ms": 999},
		{"openai_ws_latency_switch_slow_ms": 300001},
		{"openai_ws_latency_switch_min_tps": -0.1},
		{"openai_ws_latency_switch_probe_count": 0},
		{"openai_ws_latency_switch_probe_count": 1},
		{"openai_ws_latency_switch_probe_count": 21},
		{"openai_ws_latency_switch_wait_seconds": 0},
		{"openai_ws_latency_switch_wait_seconds": 3601},
	} {
		h, _ := newStepUpSwitchTestHandler(t, nil)
		rec := doUpdateSettings(t, h, fields, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

func TestUpdateSettingsPreparedSessionPoolSizePersists(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, nil)
	for _, count := range []int{1, 12, 100} {
		rec := doUpdateSettings(t, h, map[string]any{"openai_session_pool_size": count}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		settings, err := h.settingService.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.Equal(t, count, settings.OpenAISessionPoolSize)
		require.Equal(t, fmt.Sprint(count), repo.values[service.SettingKeyOpenAISessionPoolSize])
	}
	rec := doUpdateSettings(t, h, map[string]any{"openai_ws_latency_switch_enabled": false}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "100", repo.values[service.SettingKeyOpenAISessionPoolSize])
	for _, count := range []int{0, -1, 101} {
		rec = doUpdateSettings(t, h, map[string]any{"openai_session_pool_size": count}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

func TestUpdateSettingsSessionPoolFirstTokenTimeoutPersistsAndValidates(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, nil)
	defaults, err := h.settingService.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 10, defaults.OpenAISessionPoolFirstTokenTimeoutSeconds)
	for _, seconds := range []int{1, 20, 600} {
		rec := doUpdateSettings(t, h, map[string]any{"openai_session_pool_first_token_timeout_seconds": seconds}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, fmt.Sprint(seconds), repo.values[service.SettingKeyOpenAISessionPoolFirstTokenTimeoutSeconds])
		require.Contains(t, rec.Body.String(), fmt.Sprintf(`"openai_session_pool_first_token_timeout_seconds":%d`, seconds))
		require.Equal(t, seconds, h.settingService.GetOpenAISessionPoolFirstTokenTimeoutSeconds(context.Background()))
	}
	rec := doUpdateSettings(t, h, map[string]any{"openai_session_pool_size": 12}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "600", repo.values[service.SettingKeyOpenAISessionPoolFirstTokenTimeoutSeconds])
	for _, invalid := range []any{-1, 0, 601, 1.5, "20"} {
		rec = doUpdateSettings(t, h, map[string]any{"openai_session_pool_first_token_timeout_seconds": invalid}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, "600", repo.values[service.SettingKeyOpenAISessionPoolFirstTokenTimeoutSeconds])
	}
}

func TestUpdateSettingsPreparedSessionPoolSwitchDefaultsOffAndPersists(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, nil)
	settings, err := h.settingService.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.OpenAISessionPoolEnabled)
	for _, enabled := range []bool{true, false} {
		rec := doUpdateSettings(t, h, map[string]any{"openai_session_pool_enabled": enabled}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, fmt.Sprint(enabled), repo.values[service.SettingKeyOpenAISessionPoolEnabled])
		require.Contains(t, rec.Body.String(), fmt.Sprintf(`"openai_session_pool_enabled":%t`, enabled))
		require.Equal(t, enabled, h.settingService.IsOpenAISessionPoolEnabled(context.Background()))
		rec = doUpdateSettings(t, h, map[string]any{"openai_session_pool_size": 9}, nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, fmt.Sprint(enabled), repo.values[service.SettingKeyOpenAISessionPoolEnabled], "保存其他设置不能改变开关")
	}
}
