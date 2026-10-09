package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicTransitCachePolicySettingsRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: map[string]string{}}
	svc := service.NewSettingService(repo, &config.Config{})
	h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)

	getRecorder := httptest.NewRecorder()
	getContext, _ := gin.CreateTestContext(getRecorder)
	getContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/public-transit-cache", nil)
	h.GetPublicTransitCachePolicy(getContext)
	require.Equal(t, http.StatusOK, getRecorder.Code)
	require.Contains(t, getRecorder.Body.String(), `"enabled":true`)
	require.Contains(t, getRecorder.Body.String(), `"maximum_rate":92`)
	require.Contains(t, getRecorder.Body.String(), `"increase_percent":10`)
	require.Contains(t, getRecorder.Body.String(), `"low_rate_threshold":75`)
	require.Contains(t, getRecorder.Body.String(), `"low_rate_display_min":75`)
	require.Contains(t, getRecorder.Body.String(), `"low_rate_display_max":80`)
	require.Contains(t, getRecorder.Body.String(), `"group_increase_percent":{}`)

	body, err := json.Marshal(service.PublicTransitCachePolicy{
		Enabled: true, IncreasePercent: 12.5, GroupIncreasePercent: map[int64]float64{42: 37.5},
		MaximumRate: 91, LowRateThreshold: 72, LowRateDisplayMin: 82, LowRateDisplayMax: 84,
	})
	require.NoError(t, err)
	putRecorder := httptest.NewRecorder()
	putContext, _ := gin.CreateTestContext(putRecorder)
	putContext.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/public-transit-cache", bytes.NewReader(body))
	putContext.Request.Header.Set("Content-Type", "application/json")
	h.UpdatePublicTransitCachePolicy(putContext)
	require.Equal(t, http.StatusOK, putRecorder.Code)
	require.Equal(t, "true", repo.values[service.SettingKeyPublicTransitCacheEnabled])
	require.Equal(t, "91", repo.values[service.SettingKeyPublicTransitCacheMaximumRate])
	require.Equal(t, "12.5", repo.values[service.SettingKeyPublicTransitCacheIncreasePercent])
	require.Equal(t, "72", repo.values[service.SettingKeyPublicTransitCacheLowRateThreshold])
	require.Equal(t, "82", repo.values[service.SettingKeyPublicTransitCacheLowRateDisplayMin])
	require.Equal(t, "84", repo.values[service.SettingKeyPublicTransitCacheLowRateDisplayMax])
	require.JSONEq(t, `{"42":37.5}`, repo.values[service.SettingKeyPublicTransitCacheGroupIncreasePercent])

	updatedGetRecorder := httptest.NewRecorder()
	updatedGetContext, _ := gin.CreateTestContext(updatedGetRecorder)
	updatedGetContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/public-transit-cache", nil)
	h.GetPublicTransitCachePolicy(updatedGetContext)
	require.Equal(t, http.StatusOK, updatedGetRecorder.Code)
	require.Contains(t, updatedGetRecorder.Body.String(), `"group_increase_percent":{"42":37.5}`)

	invalidBody := []byte(`{"enabled":true,"maximum_rate":92,"low_rate_threshold":75,"low_rate_display_min":80,"low_rate_display_max":79}`)
	invalidRecorder := httptest.NewRecorder()
	invalidContext, _ := gin.CreateTestContext(invalidRecorder)
	invalidContext.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/public-transit-cache", bytes.NewReader(invalidBody))
	invalidContext.Request.Header.Set("Content-Type", "application/json")
	h.UpdatePublicTransitCachePolicy(invalidContext)
	require.Equal(t, http.StatusBadRequest, invalidRecorder.Code)
	require.Equal(t, "91", repo.values[service.SettingKeyPublicTransitCacheMaximumRate])
}

func TestPublicTransitCachePolicyReadsLegacyLowRateSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: map[string]string{
		service.SettingKeyPublicTransitCacheMaximumRate: "80",
		service.SettingKeyPublicTransitCacheLowRateMin:  "75",
		service.SettingKeyPublicTransitCacheLowRateMax:  "80",
	}}
	svc := service.NewSettingService(repo, &config.Config{})
	h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/public-transit-cache", nil)
	h.GetPublicTransitCachePolicy(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"maximum_rate":80`)
	require.Contains(t, recorder.Body.String(), `"low_rate_threshold":75`)
	require.Contains(t, recorder.Body.String(), `"low_rate_display_min":75`)
	require.Contains(t, recorder.Body.String(), `"low_rate_display_max":80`)
	require.NotContains(t, recorder.Body.String(), `"low_rate_min"`)
	require.NotContains(t, recorder.Body.String(), `"low_rate_max"`)
}
