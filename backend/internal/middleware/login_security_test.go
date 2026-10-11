package middleware

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestLoginLimiterRejectsSpoofedAddresses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, proxy := range []bool{false, true} {
		t.Run(fmt.Sprintf("proxy=%t", proxy), func(t *testing.T) {
			srv := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			r := gin.New()
			proxies := []string{}
			peer := "40.160.145.30:1234"
			if proxy {
				proxies = []string{"172.19.0.1"}
				peer = "172.19.0.1:1234"
			}
			require.NoError(t, r.SetTrustedProxies(proxies))
			r.Use(func(c *gin.Context) {
				ippkg.SetForwardedIPSettings(c, true, nil, ippkg.CompileIPRules(proxies))
				c.Next()
			})
			r.POST("/login", NewRateLimiter(rdb).LimitWithOptions("auth-login", 20, time.Minute, RateLimitOptions{FailureMode: RateLimitFailClose}), func(c *gin.Context) { c.Status(401) })
			for i := 1; i <= 21; i++ {
				req := httptest.NewRequest("POST", "/login", nil)
				req.RemoteAddr = peer
				req.Header.Set("CF-Connecting-IP", fmt.Sprintf("198.51.100.%d", i))
				req.Header.Set("X-Real-IP", "40.160.145.30")
				req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d, 40.160.145.30", i))
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if i <= 20 {
					require.Equal(t, 401, w.Code)
				} else {
					require.Equal(t, 429, w.Code)
				}
			}
			require.Equal(t, []string{"rate_limit:auth-login:40.160.145.30"}, srv.Keys())
		})
	}
}

func TestLoginEmailLimiterSurvivesAddressRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.POST("/login", NewRateLimiter(rdb).LimitLoginByEmail(10, time.Minute), func(c *gin.Context) {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Captcha  string `json:"turnstile_token"`
		}
		require.NoError(t, c.ShouldBindBodyWith(&req, binding.JSON))
		require.Equal(t, "secret", req.Password)
		require.Equal(t, "captcha-proof", req.Captcha)
		c.Status(http.StatusUnauthorized)
	})
	send := func(email string, n int) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"email":%q,"password":"secret","turnstile_token":"captcha-proof"}`, email)
		req := httptest.NewRequest("POST", "/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", n)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for i := 1; i <= 10; i++ {
		email := "victim@example.com"
		if i%2 == 0 {
			email = "VICTIM@EXAMPLE.COM"
		}
		require.Equal(t, 401, send(email, i).Code)
	}
	w := send("victim@example.com", 11)
	require.Equal(t, 429, w.Code)
	require.Equal(t, "60", w.Header().Get("Retry-After"))
	require.Equal(t, 401, send("other@example.com", 12).Code)
	digest := sha256.Sum256([]byte("victim@example.com"))
	require.Equal(t, time.Minute, srv.TTL(fmt.Sprintf("rate_limit:auth-login-email:%x", digest)))
	srv.FastForward(time.Minute)
	require.Equal(t, 401, send("victim@example.com", 13).Code)
}

func TestLoginEmailLimiterFailsClosed(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	srv.Close()
	r := gin.New()
	r.POST("/login", NewRateLimiter(rdb).LimitLoginByEmail(10, time.Minute), func(c *gin.Context) { t.Fatal("缓存故障时不应验证密码") })
	req := httptest.NewRequest("POST", "/login", strings.NewReader(`{"email":"victim@example.com","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 429, w.Code)
}
