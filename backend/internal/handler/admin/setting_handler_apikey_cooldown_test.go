package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apiKeyCooldownHandlerRepo struct{ settingHandlerRepoStub }

func (r *apiKeyCooldownHandlerRepo) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

func TestSettingHandler_APIKeyFailureCooldownRoundTripAndLegacyCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &apiKeyCooldownHandlerRepo{settingHandlerRepoStub: settingHandlerRepoStub{values: map[string]string{
		service.SettingKeyOverloadCooldownSettings:     `{"enabled":false,"cooldown_minutes":37}`,
		service.SettingKeyRateLimit429CooldownSettings: `{"enabled":true,"cooldown_seconds":23}`,
	}}}
	svc := service.NewSettingService(repo, &config.Config{})
	handler := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)

	getRecorder := httptest.NewRecorder()
	getContext, _ := gin.CreateTestContext(getRecorder)
	getContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/api-key-failure-cooldown", nil)
	handler.GetAPIKeyFailureCooldownSettings(getContext)
	require.Equal(t, http.StatusOK, getRecorder.Code)

	var getResponse response.Response
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &getResponse))
	getData, ok := getResponse.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(service.APIKeyFailureCooldownSettingsVersion), getData["version"])
	policies, ok := getData["policies"].(map[string]any)
	require.True(t, ok)
	require.Len(t, policies, 10)

	defaults := service.DefaultAPIKeyFailureCooldownSettings()
	policy := defaults.Policies[service.APIKeyFailureRateLimit]
	policy.Cooldowns = []int{30, 10, 30}
	defaults.Policies[service.APIKeyFailureRateLimit] = policy
	body, err := json.Marshal(defaults)
	require.NoError(t, err)

	putRecorder := httptest.NewRecorder()
	putContext, _ := gin.CreateTestContext(putRecorder)
	putContext.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/api-key-failure-cooldown", bytes.NewReader(body))
	putContext.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateAPIKeyFailureCooldownSettings(putContext)
	require.Equal(t, http.StatusOK, putRecorder.Code)

	var putResponse response.Response
	require.NoError(t, json.Unmarshal(putRecorder.Body.Bytes(), &putResponse))
	putData, ok := putResponse.Data.(map[string]any)
	require.True(t, ok)
	putPolicies, ok := putData["policies"].(map[string]any)
	require.True(t, ok)
	rateLimit, ok := putPolicies[string(service.APIKeyFailureRateLimit)].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{float64(10), float64(30)}, rateLimit["cooldowns"])

	legacy529 := httptest.NewRecorder()
	legacy529Context, _ := gin.CreateTestContext(legacy529)
	legacy529Context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/overload-cooldown", nil)
	handler.GetOverloadCooldownSettings(legacy529Context)
	require.Equal(t, http.StatusOK, legacy529.Code)
	var legacy529Response response.Response
	require.NoError(t, json.Unmarshal(legacy529.Body.Bytes(), &legacy529Response))
	legacy529Data, ok := legacy529Response.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, legacy529Data["enabled"])
	require.Equal(t, float64(37), legacy529Data["cooldown_minutes"])

	legacy429 := httptest.NewRecorder()
	legacy429Context, _ := gin.CreateTestContext(legacy429)
	legacy429Context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/rate-limit-429-cooldown", nil)
	handler.GetRateLimit429CooldownSettings(legacy429Context)
	require.Equal(t, http.StatusOK, legacy429.Code)
	var legacy429Response response.Response
	require.NoError(t, json.Unmarshal(legacy429.Body.Bytes(), &legacy429Response))
	legacy429Data, ok := legacy429Response.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, legacy429Data["enabled"])
	require.Equal(t, float64(23), legacy429Data["cooldown_seconds"])
}

func TestSettingHandler_UpdateAPIKeyFailureCooldownRejectsInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &apiKeyCooldownHandlerRepo{settingHandlerRepoStub: settingHandlerRepoStub{values: map[string]string{}}}
	handler := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), nil, nil, nil, nil, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/api-key-failure-cooldown", bytes.NewBufferString(`{"version":2,"policies":{}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateAPIKeyFailureCooldownSettings(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, repo.values[service.SettingKeyAPIKeyFailureCooldownSettings])
}
