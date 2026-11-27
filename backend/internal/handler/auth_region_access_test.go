package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type loginRegionSettingRepo struct {
	service.SettingRepository
	value string
}

func (r *loginRegionSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyRegionAccessSettings {
		return r.value, nil
	}
	return "", service.ErrSettingNotFound
}

func TestLoginRegionPrecheckUsesTrustedClientAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, peer, forwarded string
		trusted               bool
		status                int
		allowed               bool
		reason                string
	}{
		{"mainland cannot forge overseas", "223.5.5.5:1234", "8.8.8.8", false, 200, false, "MAINLAND_ACCESS_DENIED"},
		{"overseas cannot be blocked by forged header", "8.8.8.8:1234", "223.5.5.5", false, 200, true, ""},
		{"trusted proxy resolves mainland", "127.0.0.1:1234", "223.5.5.5", true, 200, false, "MAINLAND_ACCESS_DENIED"},
		{"unknown is unavailable", "127.0.0.1:1234", "", false, 503, false, "REGION_LOOKUP_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			h := &AuthHandler{settingSvc: service.NewSettingService(&loginRegionSettingRepo{value: `{"enabled":true}`}, cfg)}
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(nil))
			if tc.trusted {
				require.NoError(t, r.SetTrustedProxies([]string{"127.0.0.1"}))
			}
			r.Use(middleware.SessionBindingContext(cfg))
			r.GET("/api/v1/auth/region-access", h.GetLoginRegionAccess)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/region-access", nil)
			req.RemoteAddr = tc.peer
			req.Header.Set("X-Forwarded-For", tc.forwarded)
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			var result struct {
				Reason string `json:"reason"`
				Data   struct {
					Allowed bool   `json:"allowed"`
					Reason  string `json:"reason"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			require.Equal(t, tc.allowed, result.Data.Allowed)
			if tc.status == 200 {
				require.Equal(t, tc.reason, result.Data.Reason)
			} else {
				require.Equal(t, tc.reason, result.Reason)
			}
		})
	}
}
