package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *OpsHandler) requireQQMonitoring(c *gin.Context) bool {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return false
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	return true
}

func (h *OpsHandler) GetQQNotificationConfig(c *gin.Context) {
	if !h.requireQQMonitoring(c) {
		return
	}
	cfg, err := h.opsService.GetQQNotificationConfig(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to load QQ settings")
		return
	}
	response.Success(c, cfg)
}

func (h *OpsHandler) UpdateQQNotificationConfig(c *gin.Context) {
	if !h.requireQQMonitoring(c) {
		return
	}
	var req service.OpsQQNotificationUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	cfg, err := h.opsService.UpdateQQNotificationConfig(c.Request.Context(), &req)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, cfg)
}

func (h *OpsHandler) TestQQNotification(c *gin.Context) {
	if !h.requireQQMonitoring(c) {
		return
	}
	if err := h.opsService.TestQQNotification(c.Request.Context()); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"sent": true})
}
