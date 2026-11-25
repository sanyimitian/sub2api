package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type regionAccessHandlerRepo struct {
	service.SettingRepository
	value    string
	exists   bool
	setCalls int
	err      error
}

func (r *regionAccessHandlerRepo) GetValue(context.Context, string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if !r.exists {
		return "", service.ErrSettingNotFound
	}
	return r.value, nil
}

func (r *regionAccessHandlerRepo) Set(_ context.Context, _, value string) error {
	if r.err != nil {
		return r.err
	}
	r.setCalls++
	r.value, r.exists = value, true
	return nil
}

func newRegionAccessHandlerRouter(repo *regionAccessHandlerRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewSettingHandler(service.NewSettingService(repo, &config.Config{}), nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/region-access", h.GetRegionAccessSettings)
	router.PUT("/region-access", h.UpdateRegionAccessSettings)
	return router
}

func regionAccessHandlerRequest(router *gin.Engine, method, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/region-access", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestRegionAccessHandler_ExplicitBooleanRequired(t *testing.T) {
	for _, body := range []string{"", `{}`, `null`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":1}`} {
		t.Run(body, func(t *testing.T) {
			repo := &regionAccessHandlerRepo{value: `{"enabled":true}`, exists: true}
			rec := regionAccessHandlerRequest(newRegionAccessHandlerRouter(repo), http.MethodPut, body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Zero(t, repo.setCalls)
		})
	}
}

func TestRegionAccessHandler_DefaultAndRoundTrip(t *testing.T) {
	repo := &regionAccessHandlerRepo{}
	router := newRegionAccessHandlerRouter(repo)
	rec := regionAccessHandlerRequest(router, http.MethodGet, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"enabled":false`)
	for _, enabled := range []bool{true, false} {
		body, err := json.Marshal(service.RegionAccessSettings{Enabled: enabled})
		require.NoError(t, err)
		rec = regionAccessHandlerRequest(router, http.MethodPut, string(body))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var result struct {
			Data service.RegionAccessSettings `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		require.Equal(t, enabled, result.Data.Enabled)
		rec = regionAccessHandlerRequest(router, http.MethodGet, "")
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		require.Equal(t, enabled, result.Data.Enabled)
	}
}

func TestRegionAccessHandler_RepositoryFailureIsServerError(t *testing.T) {
	repo := &regionAccessHandlerRepo{err: errors.New("database unavailable")}
	router := newRegionAccessHandlerRouter(repo)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec := regionAccessHandlerRequest(router, method, `{"enabled":false}`)
		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	}
}
