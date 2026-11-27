package middleware

import (
	"bytes"
	"context"
	_ "embed"
	"html/template"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type siteRegionChecker interface {
	CheckSiteRegion(context.Context, string) error
}

//go:embed site_region_blocked.html
var siteRegionBlockedHTML string

var siteRegionBlockedTemplate = template.Must(template.New("site-region-blocked").Parse(siteRegionBlockedHTML))

// SiteRegionGuard 在全部业务入口之前执行，不依赖登录状态或客户端传来的角色。
func SiteRegionGuard(checker siteRegionChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 健康检查不提供业务能力；保留探活，避免地域限制触发容器重启。
		if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		err := checker.CheckSiteRegion(c.Request.Context(), SecurityClientIP(c))
		if err == nil {
			c.Next()
			return
		}
		c.Header("Cache-Control", "no-store")
		status, detail := infraerrors.ToHTTP(err)
		// 普通页面直接返回完整提示，不依赖已经被拦截的脚本、图片或接口。
		if c.Request.Method == http.MethodGet && strings.Contains(c.GetHeader("Accept"), "text/html") {
			language := detail.Metadata["default_language"]
			if language != "en" {
				language = "zh"
			}
			var body bytes.Buffer
			if renderErr := siteRegionBlockedTemplate.Execute(&body, struct {
				Nonce       string
				Language    string
				Unavailable bool
			}{GetNonceFromContext(c), language, status != http.StatusForbidden}); renderErr == nil {
				c.Data(status, "text/html; charset=utf-8", body.Bytes())
				c.Abort()
				return
			}
		}
		response.ErrorFrom(c, err)
		c.Abort()
	}
}
