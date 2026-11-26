package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetRegionAccessSettings 返回地区限制开关及新用户默认语言。
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
		Enabled         *bool  `json:"enabled" binding:"required"`
		SiteWide        *bool  `json:"site_wide"`
		DefaultLanguage string `json:"default_language" binding:"omitempty,oneof=zh en"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "必须提供布尔类型的 enabled（启用开关），site_wide（全站限制）须为布尔值，default_language（默认语言）仅支持 zh（中文）或 en（英文）")
		return
	}
	// 兼容仅更新开关的旧客户端，不重置已保存的默认语言。
	if request.DefaultLanguage == "" || request.SiteWide == nil {
		current, err := h.settingService.GetRegionAccessSettings(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if request.DefaultLanguage == "" {
			request.DefaultLanguage = current.DefaultLanguage
		}
		if request.SiteWide == nil {
			request.SiteWide = &current.SiteWide
		}
	}
	settings := &service.RegionAccessSettings{Enabled: *request.Enabled, SiteWide: *request.SiteWide, DefaultLanguage: request.DefaultLanguage}
	if err := h.settingService.SetRegionAccessSettings(c.Request.Context(), settings); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}
