package routes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newAuthRoutesTestRouter(redisClient *redis.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	v1 := router.Group("/api/v1")

	RegisterAuthRoutes(
		v1,
		&handler.Handlers{
			Auth:    &handler.AuthHandler{},
			Setting: &handler.SettingHandler{},
		},
		servermiddleware.JWTAuthMiddleware(func(c *gin.Context) {
			c.Next()
		}),
		servermiddleware.AuditLogMiddleware(func(c *gin.Context) {
			c.Next()
		}),
		redisClient,
		nil,
		nil,
	)

	return router
}

func TestAuthLoginRouteLimitsEmailAcrossIPs(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	router := newAuthRoutesTestRouter(rdb)
	for i := 1; i <= 11; i++ {
		// 可选验证码字段类型错误：通过邮箱限流后应进入真实登录处理器的校验。
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"victim@example.com","password":"secret","turnstile_token":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", i)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if i <= 10 {
			require.Equal(t, http.StatusBadRequest, w.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, w.Code)
		}
	}
}

func TestPasswordLoginRoutesShareAccountLimit(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	router := newAuthRoutesTestRouter(rdb)
	paths := []string{
		"/login", "/oauth/pending/bind-login", "/oauth/linuxdo/bind-login",
		"/oauth/wechat/bind-login", "/oauth/oidc/bind-login", "/oauth/dingtalk/bind-login",
	}
	for i := 0; i < 16; i++ {
		// adoption 字段类型错误让绑定处理器在触及数据库前返回 400。
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth"+paths[i%len(paths)], strings.NewReader(`{"email":"victim@example.com","password":"secret","turnstile_token":{},"adopt_avatar":"invalid"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", i+1)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if i < 10 {
			require.Equal(t, http.StatusBadRequest, w.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, w.Code, "path=%s", paths[i%len(paths)])
		}
	}
}

func TestAuthRoutesRateLimitFailCloseWhenRedisUnavailable(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	router := newAuthRoutesTestRouter(rdb)
	paths := []string{
		"/api/v1/auth/register",
		"/api/v1/auth/login",
		"/api/v1/auth/login/2fa",
		"/api/v1/auth/send-verify-code",
		"/api/v1/auth/oauth/pending/send-verify-code",
	}

	for _, path := range paths {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.10:12345"

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code, "path=%s", path)
		require.Contains(t, w.Body.String(), "rate limit exceeded", "path=%s", path)
	}
}
