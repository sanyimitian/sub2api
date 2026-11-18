//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
