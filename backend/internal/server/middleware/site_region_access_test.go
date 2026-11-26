package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type siteRegionRepo struct {
	service.SettingRepository
	raw string
	err error
}

func (r *siteRegionRepo) GetValue(context.Context, string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if r.raw == "" {
		return "", service.ErrSettingNotFound
	}
	return r.raw, nil
}
func (r *siteRegionRepo) Set(_ context.Context, _, value string) error { r.raw = value; return nil }

func newSiteRegionRouter(t *testing.T, repo *siteRegionRepo, trusted bool) (*gin.Engine, *service.SettingService, *int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	svc := service.NewSettingService(repo, cfg)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	if trusted {
		require.NoError(t, r.SetTrustedProxies([]string{"127.0.0.1"}))
	}
	r.Use(SessionBindingContext(cfg), SecurityHeaders(cfg.Security.CSP, nil), SiteRegionGuard(svc))
	hits := new(int)
	// 模拟统一中间件之后的静态页面和已认证业务处理，不再重复校验地区。
	r.NoRoute(func(c *gin.Context) { *hits++; c.String(http.StatusOK, "business content") })
	return r, svc, hits
}
func siteRegionRequest(r *gin.Engine, method, path, peer, forwarded, accept, credential string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = peer
	req.Header.Set("X-Forwarded-For", forwarded)
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", credential)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSiteRegionGuard_AllBusinessSurfacesAndExistingSessions(t *testing.T) {
	paths := []string{"/", "/home", "/admin/settings", "/assets/main.js", "/api/v1/auth/me", "/api/v1/users/me", "/api/v1/admin/settings/region-access", "/api/v1/auth/refresh", "/api/v1/settings/public", "/api/v1/site/region-access", "/v1/messages", "/v1/chat/completions", "/responses", "/v1beta/models/test:generateContent", "/api/v1/payments/webhook/test"}
	for _, mode := range []struct {
		raw    string
		status int
	}{
		{"", 200},
		{`{"enabled":true}`, 200},
		{`{"enabled":false,"site_wide":true}`, 200},
		{`{"enabled":true,"site_wide":true,"default_language":"en"}`, 403},
	} {
		for _, path := range paths {
			for _, credential := range []string{"", "Bearer existing-user-session", "Bearer existing-admin-session", "Bearer api-key"} {
				r, _, hits := newSiteRegionRouter(t, &siteRegionRepo{raw: mode.raw}, false)
				rec := siteRegionRequest(r, http.MethodGet, path, "223.5.5.5:1234", "8.8.8.8", "application/json", credential)
				require.Equal(t, mode.status, rec.Code, "%s: %s", path, rec.Body.String())
				if mode.status == 403 {
					require.Zero(t, *hits, "必须在业务处理之前拒绝")
					require.Contains(t, rec.Body.String(), `"scope":"site"`)
					require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
				} else {
					require.Equal(t, 1, *hits)
				}
			}
		}
	}
}

func TestSiteRegionGuard_TrustedProxyAndUnknownRegion(t *testing.T) {
	for _, tc := range []struct {
		peer, forwarded string
		trusted         bool
		status          int
	}{
		{"223.5.5.5:1", "8.8.8.8", false, 403},
		{"8.8.8.8:1", "223.5.5.5", false, 200},
		{"127.0.0.1:1", "223.5.5.5", true, 403},
		{"127.0.0.1:1", "8.8.8.8", true, 200},
		{"127.0.0.1:1", "", false, 503},
	} {
		r, _, hits := newSiteRegionRouter(t, &siteRegionRepo{raw: `{"enabled":true,"site_wide":true}`}, tc.trusted)
		rec := siteRegionRequest(r, http.MethodPost, "/v1/messages", tc.peer, tc.forwarded, "application/json", "Bearer existing-session")
		require.Equal(t, tc.status, rec.Code, rec.Body.String())
		if tc.status != 200 {
			require.Zero(t, *hits)
		}
	}
}

func TestSiteRegionGuard_HTMLAndImmediateModeChanges(t *testing.T) {
	r, svc, hits := newSiteRegionRouter(t, &siteRegionRepo{}, false)
	request := func() *httptest.ResponseRecorder {
		return siteRegionRequest(r, http.MethodGet, "/admin/settings", "223.5.5.5:1", "", "text/html", "Bearer existing-admin")
	}
	require.Equal(t, 200, request().Code)
	require.NoError(t, svc.SetRegionAccessSettings(context.Background(), &service.RegionAccessSettings{Enabled: true, SiteWide: true, DefaultLanguage: "en"}))
	rec := request()
	require.Equal(t, 403, rec.Code)
	require.Equal(t, 1, *hits)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Body.String(), `<html lang="en">`)
	require.Contains(t, rec.Body.String(), "暂不支持当前地区")
	require.Contains(t, rec.Body.String(), "根据《生成式人工智能服务管理暂行办法》监管要求")
	require.NotContains(t, rec.Body.String(), "business content")
	require.NotContains(t, rec.Body.String(), "cac.gov.cn")
	require.NotContains(t, rec.Body.String(), "<script src=")
	require.NoError(t, svc.SetRegionAccessSettings(context.Background(), &service.RegionAccessSettings{Enabled: true}))
	require.Equal(t, 200, request().Code, "关闭全站开关后已有会话立即恢复")
}

func TestSiteRegionGuard_ConfigurationFailureAndHealth(t *testing.T) {
	for _, repo := range []*siteRegionRepo{{err: errors.New("offline")}, {raw: "bad-json"}} {
		r, _, hits := newSiteRegionRouter(t, repo, false)
		rec := siteRegionRequest(r, http.MethodGet, "/", "8.8.8.8:1", "", "text/html", "")
		require.Equal(t, 503, rec.Code)
		require.Contains(t, rec.Body.String(), "暂时无法验证访问地区")
		require.Zero(t, *hits)
		rec = siteRegionRequest(r, http.MethodGet, "/health", "127.0.0.1:1", "", "", "")
		require.Equal(t, 200, rec.Code, "探活不应因地区限制导致重启")
	}
}
