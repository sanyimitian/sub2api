package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountLatencyMonitorHandler struct {
	monitor *service.AccountLatencyMonitor
}

func NewAccountLatencyMonitorHandler(monitor *service.AccountLatencyMonitor) *AccountLatencyMonitorHandler {
	return &AccountLatencyMonitorHandler{monitor: monitor}
}

func (h *AccountLatencyMonitorHandler) GetSettings(c *gin.Context) {
	settings, err := h.monitor.GetSettings(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, settings)
}

func (h *AccountLatencyMonitorHandler) UpdateSettings(c *gin.Context) {
	var settings service.AccountLatencyMonitorSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.monitor.UpdateSettings(c.Request.Context(), &settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, settings)
}

func (h *AccountLatencyMonitorHandler) GetRuntime(c *gin.Context) {
	runtime, err := h.monitor.GetRuntime(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": runtime})
}
