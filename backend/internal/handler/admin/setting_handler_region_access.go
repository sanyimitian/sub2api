package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetRegionAccessSettings 返回管理员可配置的地区限制开关。
func (h *SettingHandler) GetRegionAccessSettings(c *gin.Context) {
	settings, err := h.settingService.GetRegionAccessSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateRegionAccessSettings 要求显式传入开关，避免空请求意外解除限制。
func (h *SettingHandler) UpdateRegionAccessSettings(c *gin.Context) {
	var request struct {
		Enabled *bool `json:"enabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "必须提供布尔类型的 enabled（启用开关）")
		return
	}
	settings := &service.RegionAccessSettings{Enabled: *request.Enabled}
	if err := h.settingService.SetRegionAccessSettings(c.Request.Context(), settings); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
