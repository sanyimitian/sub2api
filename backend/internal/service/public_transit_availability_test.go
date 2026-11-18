//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPublicTransitAvailability_ThresholdAndExactModelScope(t *testing.T) {
	for _, model := range []string{"deepseek-v4.1-flash", "glm-5.3"} {
		t.Run(model, func(t *testing.T) {
			a7, a15, a30 := 84.999, 85.0, 99.0
			adjustment := adjustPublicTransitAvailability(model, &a7, &a15, &a30)
			require.NotNil(t, adjustment)
			require.GreaterOrEqual(t, a7, 88.0)
			require.LessOrEqual(t, a7, 90.0)
			require.Equal(t, 85.0, a15)
			require.Equal(t, 99.0, a30)
			require.Equal(t, 84.999, *adjustment.RawAvailability7d)
			require.Equal(t, 85.0, *adjustment.RawAvailability15d)
			require.Equal(t, 99.0, *adjustment.RawAvailability30d)
		})
	}
	for _, model := range []string{"claude-opus-5-5", "kimi-k2.5", "deepseek-v4-pro", "glm-5", "prefix/deepseek-v4.1-flash", "glm-5.3-extra", "GLM-5.3", ""} {
		t.Run("unmatched/"+model, func(t *testing.T) {
			a7, a15, a30 := 20.0, 30.0, 40.0
			require.Nil(t, adjustPublicTransitAvailability(model, &a7, &a15, &a30))
			require.Equal(t, []float64{20, 30, 40}, []float64{a7, a15, a30})
		})
	}
}

func TestPublicTransitAvailability_StableHundredthsAndOriginalValueCopies(t *testing.T) {
	for _, model := range []string{"deepseek-v4.1-flash", "glm-5.3"} {
		for i := 0; i < 85; i++ {
			raw := float64(i)
			a7, a15, a30 := raw, raw, raw
			first := adjustPublicTransitAvailability(model, &a7, &a15, &a30)
			require.NotNil(t, first)
			for _, value := range []float64{a7, a15, a30} {
				require.GreaterOrEqual(t, value, 88.0)
				require.LessOrEqual(t, value, 90.0)
				require.InDelta(t, math.Round(value*100), value*100, 1e-9)
			}
			repeat7, repeat15, repeat30 := raw, raw, raw
			second := adjustPublicTransitAvailability(model, &repeat7, &repeat15, &repeat30)
			require.Equal(t, []float64{a7, a15, a30}, []float64{repeat7, repeat15, repeat30})
			require.Equal(t, first, second)
			// Public DTO values may be reused by a caller; their metadata must
			// retain independent copies of the original measurements.
			a7, a15, a30 = 1, 2, 3
			require.Equal(t, raw, *first.RawAvailability7d)
			require.Equal(t, raw, *first.RawAvailability15d)
			require.Equal(t, raw, *first.RawAvailability30d)
		}
	}
}

func TestPublicTransitAvailability_UnchangedAndUnavailableValues(t *testing.T) {
	for _, raw := range []float64{85, 90, 100, -1, 101, math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(fmt.Sprintf("%v", raw), func(t *testing.T) {
			value := raw
			require.Nil(t, adjustPublicTransitAvailability("glm-5.3", &value, nil, nil))
			if math.IsNaN(raw) {
				require.True(t, math.IsNaN(value))
			} else {
				require.Equal(t, raw, value)
			}
		})
	}
	require.Nil(t, adjustPublicTransitAvailability("glm-5.3", nil, nil, nil))

	zero := 0.0
	adjustment := adjustPublicTransitAvailability("glm-5.3", &zero, nil, nil)
	require.NotNil(t, adjustment)
	require.NotNil(t, adjustment.RawAvailability7d)
	require.Zero(t, *adjustment.RawAvailability7d)
	require.Nil(t, adjustment.RawAvailability15d)
	require.Nil(t, adjustment.RawAvailability30d)
	encoded, err := json.Marshal(adjustment)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"raw_availability_7d":0`)
	require.NotContains(t, string(encoded), "raw_availability_15d")
	require.NotContains(t, string(encoded), "raw_availability_30d")
}

func TestPublicTransitAvailability_OnlyPublicDTOChanges(t *testing.T) {
	latency := 120000
	checkedAt := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	view := &UserMonitorView{ID: 116, Name: "DeepSeek", Provider: "openai", GroupName: "public",
		PrimaryModel: "deepseek-v4.1-flash", PrimaryStatus: "down", Availability7d: 79.0,
		PrimaryLatencyMs: &latency,
		Timeline:         []UserMonitorTimelinePoint{{Status: "down", LatencyMs: &latency, CheckedAt: checkedAt}},
		ExtraModels:      []ExtraModelStatus{{Model: "glm-5.3", Status: "down", LatencyMs: &latency}},
	}
	detail := &UserMonitorDetail{ID: view.ID, Models: []ModelDetail{
		{Model: view.PrimaryModel, LatestStatus: "down", LatestLatencyMs: &latency,
			Availability7d: 80.1, Availability15d: 84.99, Availability30d: 91.2, AvgLatency7dMs: &latency},
		{Model: "glm-5.3", LatestStatus: "down", Availability7d: 84.0, Availability15d: 90, Availability30d: 82.0},
		{Model: "claude-opus-5-5", LatestStatus: "down", Availability7d: 70, Availability15d: 71, Availability30d: 72},
	}}
	beforeView, err := json.Marshal(view)
	require.NoError(t, err)
	beforeDetail, err := json.Marshal(detail)
	require.NoError(t, err)

	monitors := buildPublicTransitMonitors([]*UserMonitorView{nil, view}, map[int64]*UserMonitorDetail{view.ID: detail}, true)
	require.Len(t, monitors, 1)
	monitor := monitors[0]
	require.Len(t, monitor.Models, 3)
	primary := monitor.Models[0]
	require.Equal(t, primary.Availability7d, monitor.Availability7d)
	require.Equal(t, primary.Availability15d, monitor.Availability15d)
	require.Equal(t, primary.Availability30d, monitor.Availability30d)
	require.Equal(t, primary.AvailabilityAdjustment, monitor.AvailabilityAdjustment)
	require.Equal(t, 80.1, *monitor.AvailabilityAdjustment.RawAvailability7d)
	require.Equal(t, "down", monitor.PrimaryStatus)
	require.Equal(t, "down", primary.LatestStatus)
	require.Equal(t, latency, *monitor.LatestLatencyMs)
	require.Equal(t, latency, *monitor.AvgLatency7dMs)
	require.Equal(t, "down", monitor.Timeline[0].Status)
	require.Equal(t, checkedAt.Format(time.RFC3339), monitor.Timeline[0].CheckedAt)
	require.Equal(t, "down", monitor.ExtraModels[0].Status)
	require.NotNil(t, monitor.Models[1].AvailabilityAdjustment)
	require.Nil(t, monitor.Models[2].AvailabilityAdjustment)
	require.Equal(t, 70.0, monitor.Models[2].Availability7d)

	afterView, err := json.Marshal(view)
	require.NoError(t, err)
	afterDetail, err := json.Marshal(detail)
	require.NoError(t, err)
	require.JSONEq(t, string(beforeView), string(afterView))
	require.JSONEq(t, string(beforeDetail), string(afterDetail))
	require.Equal(t, monitors, buildPublicTransitMonitors([]*UserMonitorView{view}, map[int64]*UserMonitorDetail{view.ID: detail}, true))
}

func TestPublicTransitAvailability_JSONDisclosesOriginalMeasurements(t *testing.T) {
	models := buildPublicTransitMonitorModels(&UserMonitorDetail{Models: []ModelDetail{
		{Model: "glm-5.3", LatestStatus: "down", Availability7d: 70.25, Availability15d: 85, Availability30d: 93},
		{Model: "deepseek-v4.1-flash", LatestStatus: "operational", Availability7d: 85, Availability15d: 90, Availability30d: 100},
	}}, true)
	encoded, err := json.Marshal(models)
	require.NoError(t, err)
	var payload []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &payload))
	metadata, ok := payload[0]["availability_adjustment"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "synthetic_below_85_v1", metadata["policy"])
	require.Contains(t, metadata["note"], "not observed availability")
	require.Contains(t, metadata["note"], "88.00%")
	require.Contains(t, metadata["note"], "90.00%")
	require.Equal(t, 70.25, metadata["raw_availability_7d"])
	require.Equal(t, 85.0, metadata["raw_availability_15d"])
	require.Equal(t, 93.0, metadata["raw_availability_30d"])
	require.NotContains(t, payload[1], "availability_adjustment")
}

func TestPublicTransitAvailability_MissingDetailDoesNotInventPeriods(t *testing.T) {
	checkedAt := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	view := &UserMonitorView{ID: 117, PrimaryModel: "glm-5.3", Availability7d: 80,
		Timeline: []UserMonitorTimelinePoint{{Status: "down", CheckedAt: checkedAt}},
	}
	monitors := buildPublicTransitMonitors([]*UserMonitorView{view}, nil, true)
	require.Len(t, monitors, 1)
	monitor := monitors[0]
	require.GreaterOrEqual(t, monitor.Availability7d, 88.0)
	require.LessOrEqual(t, monitor.Availability7d, 90.0)
	require.Zero(t, monitor.Availability15d)
	require.Zero(t, monitor.Availability30d)
	require.Empty(t, monitor.Models)
	require.NotNil(t, monitor.AvailabilityAdjustment)
	require.Equal(t, 80.0, *monitor.AvailabilityAdjustment.RawAvailability7d)
	require.Nil(t, monitor.AvailabilityAdjustment.RawAvailability15d)
	require.Nil(t, monitor.AvailabilityAdjustment.RawAvailability30d)
	require.Equal(t, 80.0, view.Availability7d)

	view.Timeline = nil
	view.Availability7d = 0
	monitors = buildPublicTransitMonitors([]*UserMonitorView{view}, nil, true)
	require.Zero(t, monitors[0].Availability7d)
	require.Nil(t, monitors[0].AvailabilityAdjustment)
	require.Empty(t, buildPublicTransitMonitorModels(nil, true))
}

func TestPublicTransitAvailability_UnobservedDetailRemainsUnavailable(t *testing.T) {
	models := buildPublicTransitMonitorModels(&UserMonitorDetail{Models: []ModelDetail{
		{Model: "deepseek-v4.1-flash"},
		{Model: "glm-5.3"},
	}}, true)
	require.Len(t, models, 2)
	for _, model := range models {
		require.Zero(t, model.Availability7d)
		require.Zero(t, model.Availability15d)
		require.Zero(t, model.Availability30d)
		require.Nil(t, model.AvailabilityAdjustment)
	}
}

func TestPublicTransitAvailability_RuntimeDefaultsOffAndFailsClosed(t *testing.T) {
	repo := &settingPublicRepoStub{values: map[string]string{}}
	settings := NewSettingService(repo, &config.Config{})
	for _, value := range []string{"", "false", "TRUE", "invalid", "true"} {
		repo.values[SettingKeyPublicTransitAvailabilityAdjustmentEnabled] = value
		require.Equal(t, value == "true", settings.GetPublicTransitRuntime(context.Background()).AvailabilityAdjustmentEnabled)
	}
	repo.values[SettingKeyPublicTransitEnabled] = "false"
	require.False(t, settings.GetPublicTransitRuntime(context.Background()).AvailabilityAdjustmentEnabled)
	repo.err = fmt.Errorf("settings unavailable")
	require.False(t, settings.GetPublicTransitRuntime(context.Background()).AvailabilityAdjustmentEnabled)
}

type availabilitySwitchMonitorRepo struct {
	adminOnlyMonitorRepo
}

func (*availabilitySwitchMonitorRepo) ListLatestPerModel(context.Context, int64) ([]*ChannelMonitorLatest, error) {
	return []*ChannelMonitorLatest{{Model: "glm-5.3", Status: MonitorStatusError}}, nil
}

func (*availabilitySwitchMonitorRepo) ComputeAvailability(context.Context, int64, int) ([]*ChannelMonitorAvailability, error) {
	return []*ChannelMonitorAvailability{{Model: "glm-5.3", AvailabilityPct: 80}}, nil
}

func TestPublicTransitAvailability_SnapshotSwitchAppliesAndRevertsWithoutRestart(t *testing.T) {
	repo := &settingPublicRepoStub{values: map[string]string{
		SettingKeyChannelMonitorEnabled: "true",
		SettingKeyChannelMonitorMode:    ChannelMonitorModeV1,
	}}
	settings := NewSettingService(repo, &config.Config{})
	groups := &stubGroupRepoForAvailable{}
	channels := NewChannelService(&mockChannelRepository{listAllFn: func(context.Context) ([]Channel, error) {
		return nil, nil
	}}, groups, nil, nil, nil)
	monitorRepo := &availabilitySwitchMonitorRepo{adminOnlyMonitorRepo: adminOnlyMonitorRepo{monitors: []*ChannelMonitor{
		{ID: 117, Enabled: true, PrimaryModel: "glm-5.3"},
	}}}
	svc := NewPublicTransitService(channels, NewChannelMonitorService(monitorRepo, nil), settings,
		NewPaymentConfigService(nil, repo, nil), groups, nil)
	for _, value := range []string{"", "true", "false", "true", "false"} {
		repo.values[SettingKeyPublicTransitAvailabilityAdjustmentEnabled] = value
		v1, err := svc.Snapshot(context.Background(), "https://example.test")
		require.NoError(t, err)
		v2, err := svc.SnapshotV2Range(context.Background(), "https://example.test", "24h")
		require.NoError(t, err)
		require.Equal(t, v1.Monitoring, v2.Monitoring)
		require.Len(t, v1.Monitoring, 1)
		item := v1.Monitoring[0]
		if value == "true" {
			require.GreaterOrEqual(t, item.Availability7d, 88.0)
			require.LessOrEqual(t, item.Availability7d, 90.0)
			require.NotNil(t, item.AvailabilityAdjustment)
			require.Equal(t, 80.0, *item.AvailabilityAdjustment.RawAvailability7d)
		} else {
			require.Equal(t, 80.0, item.Availability7d)
			require.Equal(t, 80.0, item.Availability15d)
			require.Equal(t, 80.0, item.Availability30d)
			require.Nil(t, item.AvailabilityAdjustment)
			require.Nil(t, item.Models[0].AvailabilityAdjustment)
		}
	}
}
