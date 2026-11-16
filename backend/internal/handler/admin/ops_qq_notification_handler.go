package admin

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"time"
)

// ReceiveQQWebhook is public. QQ uses Ed25519 authentication rather than an
// administrator session; replies must use QQ's protocol without our API envelope.
func (h *OpsHandler) ReceiveQQWebhook(c *gin.Context) {
	if h.opsService == nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.OpsQQWebhookBodyLimit)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.Status(http.StatusRequestEntityTooLarge)
		} else {
			c.Status(http.StatusBadRequest)
		}
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	result, err := h.opsService.HandleQQWebhook(ctx, c.GetHeader("X-Bot-Appid"), c.GetHeader("X-Signature-Timestamp"), c.GetHeader("X-Signature-Ed25519"), body)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrOpsQQWebhookUnavailable):
			status = http.StatusServiceUnavailable
		case errors.Is(err, service.ErrOpsQQWebhookUnauthorized):
			status = http.StatusUnauthorized
		case errors.Is(err, service.ErrOpsQQWebhookPayload):
			status = http.StatusBadRequest
		}
		c.Status(status)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *OpsHandler) GetQQWebhookStatus(c *gin.Context) {
	if !h.requireQQMonitoring(c) {
		return
	}
	status, err := h.opsService.GetQQWebhookStatus(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to load QQ callback status")
		return
	}
	response.Success(c, status)
}

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
