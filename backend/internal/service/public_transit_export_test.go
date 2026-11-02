//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterPublicTransitSnapshot_HidesSectionsAndNestedCacheData(t *testing.T) {
	sections := DefaultPublicTransitExportSections()
	sections.Billing = false
	sections.Cache = false
	sections.PassiveMonitoring = false
	snapshot := map[string]any{
		"schema_version": "ai-transit.v2",
		"station":        map[string]any{"name": "example"},
		"endpoints":      map[string]any{"snapshot_url": "https://example.test/api/public/transit/snapshot"},
		"billing":        map[string]any{"recharge_ratio": "1:1"},
		"cache":          map[string]any{"supported": true},
		"groups": []any{map[string]any{
			"name": "public", "cache_usage": map[string]any{"cache_hit_rate": 80},
			"models": []any{map[string]any{"price": map[string]any{"input_usd_per_token": 1, "cache_read_usd_per_token": 0.1}}},
		}},
		"passive_monitoring": map[string]any{"enabled": true},
	}

	filtered, err := FilterPublicTransitSnapshot(snapshot, sections)
	require.NoError(t, err)
	encoded, err := json.Marshal(filtered)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"billing"`)
	require.NotContains(t, string(encoded), `"cache"`)
	require.NotContains(t, string(encoded), `"cache_usage"`)
	require.NotContains(t, string(encoded), `"cache_read_usd_per_token"`)
	require.NotContains(t, string(encoded), `"passive_monitoring"`)
	require.Contains(t, string(encoded), `"snapshot_url"`)
	require.Contains(t, string(encoded), `"input_usd_per_token"`)

	complete, err := FilterPublicTransitSnapshot(snapshot, DefaultPublicTransitExportSections())
	require.NoError(t, err)
	require.Contains(t, complete, "billing")
	require.Contains(t, complete, "passive_monitoring")
}

func TestPublicTransitMetricRanges_KeepPricingAndFallbackToRealValues(t *testing.T) {
	fields := PublicTransitMetricCatalog()
	for _, path := range []string{
		"monitoring.*.availability_7d", "passive_monitoring.window.request_count",
		"groups.*.cache_usage.total.cache_hit_rate", "cache.hit_rate",
	} {
		require.Equal(t, path, metricFieldForTest(fields, path).Path)
	}
	minRate, maxRate := 95.0, 99.0
	minCount, maxCount := 10.0, 13.0
	minCache, maxCache := 50.0, 80.0
	sections := DefaultPublicTransitExportSections()
	sections.Metrics = map[string]PublicTransitMetricRange{
		"monitoring.*.availability_7d":        {Enabled: true, Min: &minRate, Max: &maxRate},
		"passive_monitoring.window.request_count": {Enabled: true, Min: &minCount, Max: &maxCount},
		"groups.*.cache_usage.total.cache_hit_rate": {Enabled: true, Min: &minCache, Max: &maxCache},
		"monitoring.*.latest_latency_ms":       {Enabled: false, Min: &minCount, Max: &maxCount},
		"passive_monitoring.window.error_count": {Enabled: true},
	}
	snapshot := map[string]any{
		"billing": map[string]any{"recharge_multiplier": 1.5},
		"groups": []any{map[string]any{
			"cache_usage": map[string]any{"total": map[string]any{"cache_hit_rate": 40.0}},
			"models": []any{map[string]any{"price": map[string]any{"input_usd_per_token": 0.2}}},
		}},
		"monitoring": []any{map[string]any{"availability_7d": 70.0, "latest_latency_ms": 250}},
		"passive_monitoring": map[string]any{"window": map[string]any{"request_count": 3, "error_count": 2}},
	}
	svc := &PublicTransitService{}
	for i := 0; i < 8; i++ {
		result, err := FilterPublicTransitSnapshot(snapshot, sections)
		require.NoError(t, err)
		require.NoError(t, svc.ApplyPublicTransitMetricRanges(context.Background(), result, sections.Metrics))
		monitor := result["monitoring"].([]any)[0].(map[string]any)
		rate := monitor["availability_7d"].(float64)
		require.GreaterOrEqual(t, rate, minRate)
		require.LessOrEqual(t, rate, maxRate)
		require.Equal(t, json.Number("250"), monitor["latest_latency_ms"])
		window := result["passive_monitoring"].(map[string]any)["window"].(map[string]any)
		count := window["request_count"].(int64)
		require.GreaterOrEqual(t, count, int64(minCount))
		require.LessOrEqual(t, count, int64(maxCount))
		require.Equal(t, json.Number("2"), window["error_count"])
		require.Equal(t, json.Number("1.5"), result["billing"].(map[string]any)["recharge_multiplier"])
		group := result["groups"].([]any)[0].(map[string]any)
		cache := group["cache_usage"].(map[string]any)["total"].(map[string]any)["cache_hit_rate"].(float64)
		require.GreaterOrEqual(t, cache, minCache)
		require.LessOrEqual(t, cache, maxCache)
		price := group["models"].([]any)[0].(map[string]any)["price"].(map[string]any)["input_usd_per_token"]
		require.Equal(t, json.Number("0.2"), price)
	}
	invalid := map[string]PublicTransitMetricRange{"billing.recharge_multiplier": {Enabled: true, Min: &minRate, Max: &maxRate}}
	require.Error(t, ValidatePublicTransitMetricRanges(invalid))
	invalid = map[string]PublicTransitMetricRange{"passive_monitoring.window.request_count": {Enabled: true, Min: &minRate, Max: &maxRate}}
	require.NoError(t, ValidatePublicTransitMetricRanges(invalid))
	badCount := 10.5
	invalid["passive_monitoring.window.request_count"] = PublicTransitMetricRange{Enabled: true, Min: &badCount, Max: &maxCount}
	require.Error(t, ValidatePublicTransitMetricRanges(invalid))
}

func metricFieldForTest(fields []PublicTransitMetricField, path string) PublicTransitMetricField {
	for _, field := range fields {
		if field.Path == path {
			return field
		}
	}
	return PublicTransitMetricField{}
}
