package handler

import (
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// GetLoginRegionAccess 仅供登录页预检，不依赖浏览器提供的地区或角色。
// 所有账号统一检查，服务端在新会话签发处再次校验。
func (h *AuthHandler) GetLoginRegionAccess(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	err := h.settingSvc.CheckLoginRegion(c.Request.Context(), middleware.SecurityClientIP(c))
	writeRegionAccessResult(c, err)
}

// GetSiteRegionAccess 仅返回全站访问判定，供首次打开和已打开页面重新检测。
func (h *AuthHandler) GetSiteRegionAccess(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	err := h.settingSvc.CheckSiteRegion(c.Request.Context(), middleware.SecurityClientIP(c))
	writeRegionAccessResult(c, err)
}

func writeRegionAccessResult(c *gin.Context, err error) {
	if err == nil {
		response.Success(c, gin.H{"allowed": true})
		return
	}
	status, detail := infraerrors.ToHTTP(err)
	if status == http.StatusForbidden {
		response.Success(c, gin.H{"allowed": false, "reason": detail.Reason})
		return
	}
	response.ErrorFrom(c, err)
}
