package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type PublicTransitHandler struct {
	publicTransitService *service.PublicTransitService
}

func NewPublicTransitHandler(publicTransitService *service.PublicTransitService) *PublicTransitHandler {
	return &PublicTransitHandler{publicTransitService: publicTransitService}
}

func (h *PublicTransitHandler) Discovery(c *gin.Context) {
	payload, err := h.publicTransitService.Discovery(c.Request.Context(), requestBaseURL(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, payload)
}

func (h *PublicTransitHandler) Snapshot(c *gin.Context) {
	payload, err := h.publicTransitService.Snapshot(c.Request.Context(), requestBaseURL(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, payload)
}

// SnapshotAuto serves the single mode-aware public contract. The selected
// monitor mode determines whether the response is the V1 active-probe shape
// or the V2 passive-aggregation shape.
func (h *PublicTransitHandler) SnapshotAuto(c *gin.Context) {
	payload, err := h.publicTransitService.SnapshotAuto(c.Request.Context(), requestBaseURL(c), c.Query("range"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, payload)
}

// SnapshotV2 serves the passive, real-traffic aggregation contract. V1 is
// intentionally left untouched for existing crawlers.
func (h *PublicTransitHandler) SnapshotV2(c *gin.Context) {
	payload, err := h.publicTransitService.SnapshotV2Range(c.Request.Context(), requestBaseURL(c), c.Query("range"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, payload)
}

func requestBaseURL(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	scheme := strings.TrimSpace(c.Request.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		scheme = "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
	}
	host := strings.TrimSpace(c.Request.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = c.Request.Host
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}
