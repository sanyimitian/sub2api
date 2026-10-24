//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminOnlyChannelMonitorHandlerRepo struct {
	service.ChannelMonitorRepository
	monitor *service.ChannelMonitor
}

func (r *adminOnlyChannelMonitorHandlerRepo) Create(_ context.Context, monitor *service.ChannelMonitor) error {
	monitor.ID = 7
	stored := *monitor
	r.monitor = &stored
	return nil
}
func (r *adminOnlyChannelMonitorHandlerRepo) GetByID(context.Context, int64) (*service.ChannelMonitor, error) {
	stored := *r.monitor
	return &stored, nil
}
func (r *adminOnlyChannelMonitorHandlerRepo) Update(_ context.Context, monitor *service.ChannelMonitor) error {
	stored := *monitor
	r.monitor = &stored
	return nil
}

func TestChannelMonitorAdminOnlyCreateUpdateAndResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &adminOnlyChannelMonitorHandlerRepo{}
	h := NewChannelMonitorHandler(service.NewChannelMonitorService(repo, duplicateChannelMonitorHandlerEncryptor{}))
	router := gin.New()
	router.POST("/monitors", h.Create)
	router.PUT("/monitors/:id", h.Update)
	router.GET("/monitors/:id", h.Get)
	for _, tc := range []struct {
		method, path, body string
		status             int
		adminOnly          bool
	}{
		{http.MethodPost, "/monitors", `{"name":"private","provider":"openai","endpoint":"https://8.8.8.8","api_key":"secret","primary_model":"model","interval_seconds":60,"admin_only":true}`, http.StatusCreated, true},
		{http.MethodPut, "/monitors/7", `{"name":"renamed"}`, http.StatusOK, true},
		{http.MethodGet, "/monitors/7", ``, http.StatusOK, true},
		{http.MethodPut, "/monitors/7", `{"admin_only":false}`, http.StatusOK, false},
	} {
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, tc.status, response.Code, response.Body.String())
		require.Equal(t, tc.adminOnly, repo.monitor.AdminOnly)
		var payload struct {
			Data struct {
				AdminOnly bool `json:"admin_only"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		require.Equal(t, tc.adminOnly, payload.Data.AdminOnly)
		require.NotContains(t, response.Body.String(), `"api_key":"secret"`)
	}
}
