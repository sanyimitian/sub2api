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
	require.Contains(t, getRecorder.Body.String(), `"group_increase_percent":{}`)

	body, err := json.Marshal(service.PublicTransitCachePolicy{
		Enabled: true, IncreasePercent: 12.5, GroupIncreasePercent: map[int64]float64{42: 37.5},
		MinimumRate: 82, MaximumRate: 91, LowRateMin: 72, LowRateMax: 82,
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
	require.JSONEq(t, `{"42":37.5}`, repo.values[service.SettingKeyPublicTransitCacheGroupIncreasePercent])

	updatedGetRecorder := httptest.NewRecorder()
	updatedGetContext, _ := gin.CreateTestContext(updatedGetRecorder)
	updatedGetContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/public-transit-cache", nil)
	h.GetPublicTransitCachePolicy(updatedGetContext)
	require.Equal(t, http.StatusOK, updatedGetRecorder.Code)
	require.Contains(t, updatedGetRecorder.Body.String(), `"group_increase_percent":{"42":37.5}`)

	invalidBody := []byte(`{"enabled":true,"minimum_rate":80,"maximum_rate":92,"low_rate_min":80,"low_rate_max":75}`)
	invalidRecorder := httptest.NewRecorder()
	invalidContext, _ := gin.CreateTestContext(invalidRecorder)
	invalidContext.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/public-transit-cache", bytes.NewReader(invalidBody))
	invalidContext.Request.Header.Set("Content-Type", "application/json")
	h.UpdatePublicTransitCachePolicy(invalidContext)
	require.Equal(t, http.StatusBadRequest, invalidRecorder.Code)
	require.Equal(t, "91", repo.values[service.SettingKeyPublicTransitCacheMaximumRate])
}
