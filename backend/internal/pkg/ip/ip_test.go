//go:build unit

package ip

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		peer    string
		proxies []string
		custom  []string
		enabled bool
		headers map[string]string
		want    string
	}{
		{name: "direct client cannot spoof standard or custom headers", peer: "9.9.9.9:1234", enabled: true, custom: []string{"X-CDN-IP"}, headers: map[string]string{"CF-Connecting-IP": "1.2.3.4", "X-Real-IP": "1.2.3.4", "X-Forwarded-For": "1.2.3.4", "X-CDN-IP": "1.2.3.4"}, want: "9.9.9.9"},
		{name: "trusted nginx ignores unsolicited CDN header", peer: "172.19.0.1:1234", proxies: []string{"172.19.0.1/32"}, enabled: true, headers: map[string]string{"CF-Connecting-IP": "1.2.3.4", "X-Real-IP": "40.160.145.30", "X-Forwarded-For": "1.2.3.4, 40.160.145.30"}, want: "40.160.145.30"},
		{name: "trusted custom CDN header retains priority", peer: "172.19.0.1:1234", proxies: []string{"172.19.0.1/32"}, enabled: true, custom: []string{"True-Client-IP"}, headers: map[string]string{"True-Client-IP": "203.0.113.42", "X-Forwarded-For": "40.160.145.30"}, want: "203.0.113.42"},
		{name: "custom chain stops at untrusted hop", peer: "172.19.0.1:1234", proxies: []string{"172.19.0.1/32"}, enabled: true, custom: []string{"X-CDN-IP"}, headers: map[string]string{"X-CDN-IP": "1.2.3.4, 203.0.113.42, 172.19.0.1"}, want: "203.0.113.42"},
		{name: "private untrusted hop cannot be skipped", peer: "172.19.0.1:1234", proxies: []string{"172.19.0.1/32"}, enabled: true, custom: []string{"X-CDN-IP"}, headers: map[string]string{"X-CDN-IP": "1.2.3.4, 10.0.0.2"}, want: "10.0.0.2"},
		{name: "invalid nearest custom hop falls back", peer: "172.19.0.1:1234", proxies: []string{"172.19.0.1/32"}, enabled: true, custom: []string{"X-CDN-IP"}, headers: map[string]string{"X-CDN-IP": "1.2.3.4, unknown", "X-Real-IP": "203.0.113.42"}, want: "203.0.113.42"},
		{name: "disabled switch keeps standard proxy resolution", peer: "172.19.0.1:1234", proxies: []string{"172.19.0.1/32"}, custom: []string{"X-CDN-IP"}, headers: map[string]string{"X-CDN-IP": "1.2.3.4", "X-Forwarded-For": "203.0.113.42"}, want: "203.0.113.42"},
		{name: "IPv6 direct peer cannot spoof header", peer: "[2001:db8::2]:1234", enabled: true, custom: []string{"X-CDN-IP"}, headers: map[string]string{"X-CDN-IP": "1.2.3.4"}, want: "2001:db8::2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(tt.proxies))
			r.GET("/t", func(c *gin.Context) {
				SetForwardedIPSettings(c, tt.enabled, tt.custom, CompileIPRules(tt.proxies))
				require.Equal(t, tt.want, GetSecurityClientIP(c, !tt.enabled))
				c.String(200, GetClientIP(c))
			})
			req := httptest.NewRequest("GET", "/t", nil)
			req.RemoteAddr = tt.peer
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tt.want, w.Body.String())
		})
	}
}

func TestCustomHeaderSnapshot(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies([]string{"127.0.0.1"}))
	r.GET("/t", func(c *gin.Context) {
		headers := []string{"X-Original-IP"}
		SetForwardedIPSettings(c, true, headers, CompileIPRules([]string{"127.0.0.1"}))
		headers[0] = "X-Mutated-IP"
		c.String(200, GetSecurityClientIP(c, false))
	})
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Original-IP", "1.2.3.4")
	req.Header.Set("X-Mutated-IP", "4.4.4.4")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, "1.2.3.4", w.Body.String())
}

func TestCheckIPRestrictionWithCompiledRules(t *testing.T) {
	whitelist := CompileIPRules([]string{"10.0.0.0/8", "192.168.1.2"})
	blacklist := CompileIPRules([]string{"10.1.1.1"})

	allowed, reason := CheckIPRestrictionWithCompiledRules("10.2.3.4", whitelist, blacklist)
	require.True(t, allowed)
	require.Equal(t, "", reason)

	allowed, reason = CheckIPRestrictionWithCompiledRules("10.1.1.1", whitelist, blacklist)
	require.False(t, allowed)
	require.Equal(t, "access denied", reason)
}

func TestCheckIPRestrictionWithCompiledRules_InvalidWhitelistStillDenies(t *testing.T) {
	// 与旧实现保持一致：白名单有配置但全无效时，最终应拒绝访问。
	invalidWhitelist := CompileIPRules([]string{"not-a-valid-pattern"})
	allowed, reason := CheckIPRestrictionWithCompiledRules("8.8.8.8", invalidWhitelist, nil)
	require.False(t, allowed)
	require.Equal(t, "access denied", reason)
}
