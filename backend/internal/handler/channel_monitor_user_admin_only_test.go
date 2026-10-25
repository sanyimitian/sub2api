//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userAdminOnlyMonitorRepo struct {
	service.ChannelMonitorRepository
	monitor *service.ChannelMonitor
	details int
}

func (r *userAdminOnlyMonitorRepo) ListEnabled(context.Context) ([]*service.ChannelMonitor, error) {
	return []*service.ChannelMonitor{r.monitor}, nil
}
func (r *userAdminOnlyMonitorRepo) GetByID(_ context.Context, id int64) (*service.ChannelMonitor, error) {
	if id != r.monitor.ID {
		return nil, service.ErrChannelMonitorNotFound
	}
	return r.monitor, nil
}
func (r *userAdminOnlyMonitorRepo) ListLatestForMonitorIDs(context.Context, []int64) (map[int64][]*service.ChannelMonitorLatest, error) {
	return map[int64][]*service.ChannelMonitorLatest{}, nil
}
func (r *userAdminOnlyMonitorRepo) ComputeAvailabilityForMonitors(context.Context, []int64, int) (map[int64][]*service.ChannelMonitorAvailability, error) {
	return map[int64][]*service.ChannelMonitorAvailability{}, nil
}
func (r *userAdminOnlyMonitorRepo) ListRecentHistoryForMonitors(context.Context, []int64, map[int64]string, int) (map[int64][]*service.ChannelMonitorHistoryEntry, error) {
	return map[int64][]*service.ChannelMonitorHistoryEntry{}, nil
}
func (r *userAdminOnlyMonitorRepo) ListLatestPerModel(context.Context, int64) ([]*service.ChannelMonitorLatest, error) {
	r.details++
	return []*service.ChannelMonitorLatest{}, nil
}
func (r *userAdminOnlyMonitorRepo) ComputeAvailability(context.Context, int64, int) ([]*service.ChannelMonitorAvailability, error) {
	r.details++
	return []*service.ChannelMonitorAvailability{}, nil
}

func TestChannelMonitorUserAdminOnlyEnforcesAuthenticatedRoleAndCachePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &userAdminOnlyMonitorRepo{monitor: &service.ChannelMonitor{ID: 7, Name: "private-monitor", Enabled: true, AdminOnly: true, PrimaryModel: "model"}}
	h := NewChannelMonitorUserHandler(service.NewChannelMonitorService(repo, nil), nil)
	for _, role := range []string{service.RoleAdmin, service.RoleUser, "", service.RoleAdmin, service.RoleUser} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			// Simulates verified auth middleware; query/header role claims below
			// must never override this identity.
			if role != "" {
				c.Set(string(middleware.ContextKeyUserRole), role)
			}
		})
		router.GET("/monitors", h.List)
		router.GET("/monitors/:id/status", h.GetStatus)
		for _, path := range []string{"/monitors?admin=true&admin_only=true&role=admin", "/monitors/7/status?admin=true&role=admin"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-User-Role", "admin")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
			require.Equal(t, "Authorization, Cookie", response.Header().Get("Vary"))
			if role == service.RoleAdmin {
				require.Equal(t, http.StatusOK, response.Code)
				require.Contains(t, response.Body.String(), `"admin_only":true`)
				require.Contains(t, response.Body.String(), "private-monitor")
			} else if strings.HasPrefix(path, "/monitors?") {
				require.Equal(t, http.StatusOK, response.Code)
				var payload struct {
					Data struct {
						Items []json.RawMessage `json:"items"`
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
				require.Empty(t, payload.Data.Items)
				require.NotContains(t, response.Body.String(), "private-monitor")
			} else {
				require.Equal(t, http.StatusNotFound, response.Code)
				require.NotContains(t, response.Body.String(), "private-monitor")
			}
		}
	}
	require.Equal(t, 8, repo.details, "only the two authenticated admin detail calls may fetch history")
}
