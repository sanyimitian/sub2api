package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type regionAccessRouteRepo struct {
	service.SettingRepository
	writes int
}

func (r *regionAccessRouteRepo) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}

func (r *regionAccessRouteRepo) Set(context.Context, string, string) error {
	r.writes++
	return nil
}

func TestRegionAccessSettingsRoutesRequireAdminAndStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &regionAccessRouteRepo{}
	svc := service.NewSettingService(repo, &config.Config{})
	h := &handler.Handlers{Admin: &handler.AdminHandlers{Setting: adminhandler.NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)}}
	router := gin.New()
	admin := router.Group("/api/v1/admin")
	admin.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "verified-admin" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	})
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("X-Test-Step-Up") != "verified" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	registerSettingsRoutes(admin, h, stepUp)
	for _, tc := range []struct {
		name   string
		method string
		admin  bool
		stepUp bool
		status int
	}{
		{"anonymous read", http.MethodGet, false, false, http.StatusForbidden},
		{"anonymous write", http.MethodPut, false, true, http.StatusForbidden},
		{"admin read", http.MethodGet, true, false, http.StatusOK},
		{"write requires step up", http.MethodPut, true, false, http.StatusUnauthorized},
		{"verified write", http.MethodPut, true, true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "/api/v1/admin/settings/region-access", bytes.NewBufferString(`{"enabled":true}`))
			request.Header.Set("Content-Type", "application/json")
			if tc.admin {
				request.Header.Set("Authorization", "verified-admin")
			}
			if tc.stepUp {
				request.Header.Set("X-Test-Step-Up", "verified")
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
	require.Equal(t, 1, repo.writes)
}
