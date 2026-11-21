//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicTransitAvailabilityAdjustmentSettingsRoundTrip(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	const field = "public_transit_availability_adjustment_enabled"

	assertResponse := func(rec *httptest.ResponseRecorder, want bool) {
		t.Helper()
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var response struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		require.Contains(t, response.Data, field, "settings response must expose the switch even when disabled")
		var enabled *bool
		require.NoError(t, json.Unmarshal(response.Data[field], &enabled))
		require.NotNil(t, enabled, "the switch must be a boolean, not null")
		require.Equal(t, want, *enabled)
	}
	getSettings := func() *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
		h.GetSettings(ctx)
		return rec
	}

	// Existing installations have no stored key and must remain disabled.
	assertResponse(getSettings(), false)
	require.NotContains(t, repo.values, service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled)

	assertResponse(doUpdateSettings(t, h, map[string]any{field: true}, nil), true)
	require.Equal(t, "true", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled])
	assertResponse(getSettings(), true)

	// An unrelated partial save must not treat the omitted switch as false.
	assertResponse(doUpdateSettings(t, h, map[string]any{"site_name": "Example Gateway"}, nil), true)
	require.Equal(t, "Example Gateway", repo.values[service.SettingKeySiteName])
	require.Equal(t, "true", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled])
	assertResponse(getSettings(), true)

	assertResponse(doUpdateSettings(t, h, map[string]any{field: false}, nil), false)
	require.Equal(t, "false", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled])
	assertResponse(getSettings(), false)
}

func getPublicTransitAvailabilitySettings(t *testing.T, h *SettingHandler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(ctx)
	return rec
}

func assertPublicTransitAvailabilityRange(t *testing.T, rec *httptest.ResponseRecorder, minimum, maximum float64) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	for key, want := range map[string]float64{
		service.SettingKeyPublicTransitAvailabilityAdjustmentMin: minimum,
		service.SettingKeyPublicTransitAvailabilityAdjustmentMax: maximum,
	} {
		require.Contains(t, response.Data, key)
		var value *float64
		require.NoError(t, json.Unmarshal(response.Data[key], &value))
		require.NotNil(t, value, "range endpoints must be numbers, not null")
		require.Equal(t, want, *value, key)
	}
}

func TestPublicTransitAvailabilityRangeSettingsRoundTrip(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	assertPublicTransitAvailabilityRange(t, getPublicTransitAvailabilitySettings(t, h), 88, 90)
	require.NotContains(t, repo.values, service.SettingKeyPublicTransitAvailabilityAdjustmentMin)
	require.NotContains(t, repo.values, service.SettingKeyPublicTransitAvailabilityAdjustmentMax)

	// A legacy payload which only knows the switch retains the default range.
	assertPublicTransitAvailabilityRange(t, doUpdateSettings(t, h, map[string]any{
		service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled: true,
	}, nil), 88, 90)
	require.Equal(t, "88", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMin])
	require.Equal(t, "90", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMax])

	// Either endpoint can be updated without resetting the other endpoint.
	assertPublicTransitAvailabilityRange(t, doUpdateSettings(t, h, map[string]any{
		service.SettingKeyPublicTransitAvailabilityAdjustmentMax: 92.25,
	}, nil), 88, 92.25)
	assertPublicTransitAvailabilityRange(t, doUpdateSettings(t, h, map[string]any{
		service.SettingKeyPublicTransitAvailabilityAdjustmentMin: 87.5,
	}, nil), 87.5, 92.25)
	require.Equal(t, "87.5", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMin])
	require.Equal(t, "92.25", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMax])
	assertPublicTransitAvailabilityRange(t, getPublicTransitAvailabilitySettings(t, h), 87.5, 92.25)

	assertPublicTransitAvailabilityRange(t, doUpdateSettings(t, h, map[string]any{
		"site_name": "Range Preservation Test",
	}, nil), 87.5, 92.25)
	assertPublicTransitAvailabilityRange(t, doUpdateSettings(t, h, map[string]any{
		service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled: false,
	}, nil), 87.5, 92.25)
	require.Equal(t, "false", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled])
	require.Equal(t, "87.5", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMin])
	require.Equal(t, "92.25", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMax])
	assertPublicTransitAvailabilityRange(t, getPublicTransitAvailabilitySettings(t, h), 87.5, 92.25)
}

type publicTransitAvailabilityFreshSettingsRepo struct {
	*settingHandlerRepoStub
}

func (r *publicTransitAvailabilityFreshSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, exists := r.values[key]; exists {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func TestPublicTransitAvailabilityRangeInitializationDefaults(t *testing.T) {
	repo := &publicTransitAvailabilityFreshSettingsRepo{
		settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{}},
	}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "false", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled])
	require.Equal(t, "85", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold])
	require.Equal(t, "88", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMin])
	require.Equal(t, "90", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMax])

	// Initialization is not a migration and must not reset an existing range.
	repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold] = "0"
	repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMin] = "0"
	repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMax] = "100"
	require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
	require.Equal(t, "0", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold])
	require.Equal(t, "0", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMin])
	require.Equal(t, "100", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMax])
}

func assertPublicTransitAvailabilityThreshold(t *testing.T, rec *httptest.ResponseRecorder, want float64) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var response struct {
		Data struct {
			Threshold *float64 `json:"public_transit_availability_adjustment_threshold"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.NotNil(t, response.Data.Threshold, "settings response must expose a numeric threshold, including zero")
	require.Equal(t, want, *response.Data.Threshold)
}

func TestPublicTransitAvailabilityThresholdSettingsRoundTrip(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	const key = service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold
	assertPublicTransitAvailabilityThreshold(t, getPublicTransitAvailabilitySettings(t, h), 85)
	require.NotContains(t, repo.values, key)

	// An older client can save the switch without resetting the threshold.
	assertPublicTransitAvailabilityThreshold(t, doUpdateSettings(t, h, map[string]any{
		service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled: true,
	}, nil), 85)
	require.Equal(t, "85", repo.values[key])

	for _, threshold := range []float64{77.25, 0, 100} {
		assertPublicTransitAvailabilityThreshold(t, doUpdateSettings(t, h, map[string]any{key: threshold}, nil), threshold)
		assertPublicTransitAvailabilityThreshold(t, getPublicTransitAvailabilitySettings(t, h), threshold)
		assertPublicTransitAvailabilityThreshold(t, doUpdateSettings(t, h, map[string]any{
			"site_name": "Threshold Preservation Test",
		}, nil), threshold)
		assertPublicTransitAvailabilityThreshold(t, doUpdateSettings(t, h, map[string]any{
			service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled: false,
		}, nil), threshold)
		assertPublicTransitAvailabilityThreshold(t, getPublicTransitAvailabilitySettings(t, h), threshold)
	}

	// Trigger threshold and output range are independent settings.
	assertPublicTransitAvailabilityRange(t, getPublicTransitAvailabilitySettings(t, h), 88, 90)
	require.Equal(t, "100", repo.values[key])
}

func TestPublicTransitAvailabilityThresholdRejectsInvalidWithoutWriting(t *testing.T) {
	for _, threshold := range []float64{-0.01, 100.01, 85.001} {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{
			service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled:   "true",
			service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold: "85",
			service.SettingKeySiteName:                                     "Existing Site",
		})
		before := make(map[string]string, len(repo.values))
		for key, value := range repo.values {
			before[key] = value
		}
		rec := doUpdateSettings(t, h, map[string]any{
			service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold: threshold,
			"site_name": "Must Not Be Written",
		}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "INVALID_PUBLIC_TRANSIT_AVAILABILITY_THRESHOLD")
		require.Equal(t, before, repo.values, "rejected thresholds must not partially update settings")
		require.Nil(t, repo.lastUpdates)
	}
}

func TestPublicTransitAvailabilityThresholdRejectsNonFiniteServiceValues(t *testing.T) {
	for _, threshold := range []float64{math.NaN(), math.Inf(-1), math.Inf(1)} {
		h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
		settings, err := h.settingService.GetAllSettings(context.Background())
		require.NoError(t, err)
		settings.PublicTransitAvailabilityAdjustmentThreshold = threshold
		err = h.settingService.UpdateSettings(context.Background(), settings)
		require.Error(t, err)
		require.Contains(t, err.Error(), "INVALID_PUBLIC_TRANSIT_AVAILABILITY_THRESHOLD")
		require.Empty(t, repo.values)
		require.Nil(t, repo.lastUpdates)
	}
}

func TestPublicTransitAvailabilityThresholdInvalidStoredValuesStayDisabledOnUnrelatedSave(t *testing.T) {
	for _, threshold := range []string{"bad", "NaN", "+Inf", "-1", "101", "85.001"} {
		t.Run(threshold, func(t *testing.T) {
			h, repo := newStepUpSwitchTestHandler(t, map[string]string{
				service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled:   "true",
				service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold: threshold,
			})
			settings, err := h.settingService.GetAllSettings(context.Background())
			require.NoError(t, err)
			require.False(t, settings.PublicTransitAvailabilityAdjustmentEnabled)
			require.Equal(t, float64(85), settings.PublicTransitAvailabilityAdjustmentThreshold)
			require.False(t, h.settingService.GetPublicTransitRuntime(context.Background()).AvailabilityAdjustmentEnabled)
			assertPublicTransitAvailabilityThreshold(t, getPublicTransitAvailabilitySettings(t, h), 85)

			rec := doUpdateSettings(t, h, map[string]any{"site_name": "Updated Site"}, nil)
			assertPublicTransitAvailabilityThreshold(t, rec, 85)
			require.Contains(t, rec.Body.String(), `"public_transit_availability_adjustment_enabled":false`)
			require.Equal(t, "Updated Site", repo.values[service.SettingKeySiteName])
			require.Equal(t, "false", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled])
			require.Equal(t, "85", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold])
			require.False(t, h.settingService.GetPublicTransitRuntime(context.Background()).AvailabilityAdjustmentEnabled)
		})
	}
}

func TestDiffSettingsPublicTransitAvailabilityThreshold(t *testing.T) {
	before := &service.SystemSettings{PublicTransitAvailabilityAdjustmentThreshold: 85}
	for _, threshold := range []float64{0, 77.25, 85, 100} {
		changed := diffSettings(before, &service.SystemSettings{
			PublicTransitAvailabilityAdjustmentThreshold: threshold,
		}, nil, nil, UpdateSettingsRequest{})
		if threshold == 85 {
			require.NotContains(t, changed, service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold)
		} else {
			require.Contains(t, changed, service.SettingKeyPublicTransitAvailabilityAdjustmentThreshold)
		}
		require.NotContains(t, changed, service.SettingKeyPublicTransitAvailabilityAdjustmentMin)
		require.NotContains(t, changed, service.SettingKeyPublicTransitAvailabilityAdjustmentMax)
	}
}

func TestPublicTransitAvailabilityRangeAcceptsZeroAndEqualBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		minimum float64
		maximum float64
	}{
		{name: "full_range", minimum: 0, maximum: 100},
		{name: "equal_zero", minimum: 0, maximum: 0},
		{name: "equal_decimal", minimum: 88.25, maximum: 88.25},
		{name: "equal_hundred", minimum: 100, maximum: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newStepUpSwitchTestHandler(t, map[string]string{
				service.SettingKeyPublicTransitAvailabilityAdjustmentMin: "88",
				service.SettingKeyPublicTransitAvailabilityAdjustmentMax: "90",
			})
			assertPublicTransitAvailabilityRange(t, doUpdateSettings(t, h, map[string]any{
				service.SettingKeyPublicTransitAvailabilityAdjustmentMin: tc.minimum,
				service.SettingKeyPublicTransitAvailabilityAdjustmentMax: tc.maximum,
			}, nil), tc.minimum, tc.maximum)
			assertPublicTransitAvailabilityRange(t, getPublicTransitAvailabilitySettings(t, h), tc.minimum, tc.maximum)
		})
	}
}

func TestPublicTransitAvailabilityRangeRejectsInvalidWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{name: "negative_min", body: map[string]any{service.SettingKeyPublicTransitAvailabilityAdjustmentMin: -0.01}},
		{name: "over_hundred_max", body: map[string]any{service.SettingKeyPublicTransitAvailabilityAdjustmentMax: 100.01}},
		{name: "reversed_bounds", body: map[string]any{
			service.SettingKeyPublicTransitAvailabilityAdjustmentMin: 91,
			service.SettingKeyPublicTransitAvailabilityAdjustmentMax: 89,
		}},
		{name: "min_above_saved_max", body: map[string]any{service.SettingKeyPublicTransitAvailabilityAdjustmentMin: 95}},
		{name: "max_below_saved_min", body: map[string]any{service.SettingKeyPublicTransitAvailabilityAdjustmentMax: 80}},
		{name: "min_precision", body: map[string]any{service.SettingKeyPublicTransitAvailabilityAdjustmentMin: 88.001}},
		{name: "max_precision", body: map[string]any{service.SettingKeyPublicTransitAvailabilityAdjustmentMax: 90.001}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := map[string]string{
				service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled: "true",
				service.SettingKeyPublicTransitAvailabilityAdjustmentMin:     "88",
				service.SettingKeyPublicTransitAvailabilityAdjustmentMax:     "90",
				service.SettingKeySiteName:                                   "Existing Site",
			}
			h, repo := newStepUpSwitchTestHandler(t, stored)
			before := make(map[string]string, len(stored))
			for key, value := range stored {
				before[key] = value
			}
			tc.body["site_name"] = "Must Not Be Written"
			rec := doUpdateSettings(t, h, tc.body, nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "INVALID_PUBLIC_TRANSIT_AVAILABILITY_RANGE")
			require.Equal(t, before, repo.values, "a rejected range must not partially update settings")
			require.Nil(t, repo.lastUpdates)
		})
	}
}

func TestPublicTransitAvailabilityRangeRejectsNonFiniteServiceValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		minimum float64
		maximum float64
	}{
		{name: "nan_min", minimum: math.NaN(), maximum: 90},
		{name: "nan_max", minimum: 88, maximum: math.NaN()},
		{name: "negative_infinite_min", minimum: math.Inf(-1), maximum: 90},
		{name: "positive_infinite_max", minimum: 88, maximum: math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
			settings, err := h.settingService.GetAllSettings(context.Background())
			require.NoError(t, err)
			settings.PublicTransitAvailabilityAdjustmentMin = tc.minimum
			settings.PublicTransitAvailabilityAdjustmentMax = tc.maximum
			err = h.settingService.UpdateSettings(context.Background(), settings)
			require.Error(t, err)
			require.Contains(t, err.Error(), "INVALID_PUBLIC_TRANSIT_AVAILABILITY_RANGE")
			require.Empty(t, repo.values)
			require.Nil(t, repo.lastUpdates)
		})
	}
}

func TestPublicTransitAvailabilityRangeInvalidStoredValuesFallBack(t *testing.T) {
	for _, stored := range []map[string]string{
		{service.SettingKeyPublicTransitAvailabilityAdjustmentMin: "not-a-number"},
		{service.SettingKeyPublicTransitAvailabilityAdjustmentMin: "91"},
		{service.SettingKeyPublicTransitAvailabilityAdjustmentMax: "NaN"},
		{service.SettingKeyPublicTransitAvailabilityAdjustmentMin: "88.001"},
	} {
		h, _ := newStepUpSwitchTestHandler(t, stored)
		assertPublicTransitAvailabilityRange(t, getPublicTransitAvailabilitySettings(t, h), 88, 90)
	}
}

func TestPublicTransitAvailabilityRangeInvalidStoredValuesStayDisabledOnUnrelatedSave(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled: "true",
		service.SettingKeyPublicTransitAvailabilityAdjustmentMin:     "bad",
		service.SettingKeyPublicTransitAvailabilityAdjustmentMax:     "90",
	})
	settings, err := h.settingService.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.PublicTransitAvailabilityAdjustmentEnabled)
	require.False(t, h.settingService.GetPublicTransitRuntime(context.Background()).AvailabilityAdjustmentEnabled)

	rec := doUpdateSettings(t, h, map[string]any{"site_name": "Updated Site"}, nil)
	assertPublicTransitAvailabilityRange(t, rec, 88, 90)
	require.Contains(t, rec.Body.String(), `"public_transit_availability_adjustment_enabled":false`)
	require.Equal(t, "Updated Site", repo.values[service.SettingKeySiteName])
	require.Equal(t, "false", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled])
	require.Equal(t, "88", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMin])
	require.Equal(t, "90", repo.values[service.SettingKeyPublicTransitAvailabilityAdjustmentMax])
	settings, err = h.settingService.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.PublicTransitAvailabilityAdjustmentEnabled)
	require.False(t, h.settingService.GetPublicTransitRuntime(context.Background()).AvailabilityAdjustmentEnabled)
}

func TestDiffSettingsPublicTransitAvailabilityRange(t *testing.T) {
	before := &service.SystemSettings{
		PublicTransitAvailabilityAdjustmentMin: 88,
		PublicTransitAvailabilityAdjustmentMax: 90,
	}
	for _, tc := range []struct {
		name    string
		minimum float64
		maximum float64
	}{
		{name: "minimum_only", minimum: 87, maximum: 90},
		{name: "maximum_only", minimum: 88, maximum: 91},
		{name: "both", minimum: 0, maximum: 100},
		{name: "unchanged", minimum: 88, maximum: 90},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := diffSettings(before, &service.SystemSettings{
				PublicTransitAvailabilityAdjustmentMin: tc.minimum,
				PublicTransitAvailabilityAdjustmentMax: tc.maximum,
			}, nil, nil, UpdateSettingsRequest{})
			for key, want := range map[string]bool{
				service.SettingKeyPublicTransitAvailabilityAdjustmentMin: tc.minimum != before.PublicTransitAvailabilityAdjustmentMin,
				service.SettingKeyPublicTransitAvailabilityAdjustmentMax: tc.maximum != before.PublicTransitAvailabilityAdjustmentMax,
			} {
				if want {
					require.Contains(t, changed, key)
				} else {
					require.NotContains(t, changed, key)
				}
			}
		})
	}
}

func TestDiffSettingsPublicTransitAvailabilityAdjustment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before bool
		after  bool
	}{
		{name: "enable", before: false, after: true},
		{name: "disable", before: true, after: false},
		{name: "unchanged_disabled", before: false, after: false},
		{name: "unchanged_enabled", before: true, after: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := diffSettings(
				&service.SystemSettings{PublicTransitAvailabilityAdjustmentEnabled: tc.before},
				&service.SystemSettings{PublicTransitAvailabilityAdjustmentEnabled: tc.after},
				nil,
				nil,
				UpdateSettingsRequest{},
			)
			if tc.before != tc.after {
				require.Contains(t, changed, service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled)
			} else {
				require.NotContains(t, changed, service.SettingKeyPublicTransitAvailabilityAdjustmentEnabled)
			}
		})
	}
}
