package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type PublicTransitHandler struct {
	publicTransitService publicTransitSnapshotService
}

type publicTransitSnapshotService interface {
	Discovery(context.Context, string) (*service.PublicTransitDiscovery, error)
	Snapshot(context.Context, string) (*service.PublicTransitSnapshot, error)
	SnapshotAuto(context.Context, string, string) (any, error)
	SnapshotV2Range(context.Context, string, string) (*service.PublicTransitSnapshotV2, error)
	GetExportSections(context.Context) (service.PublicTransitExportSections, error)
	SetExportSections(context.Context, service.PublicTransitExportSections) error
	PublicSnapshot(context.Context, any) (map[string]any, error)
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
	// Manual monitor visibility can change immediately after this response.
	c.Header("Cache-Control", "no-store")
	h.writePublicSnapshot(c, payload)
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
	c.Header("Cache-Control", "no-store")
	h.writePublicSnapshot(c, payload)
}

// SnapshotV2 serves the passive, real-traffic aggregation contract. V1 is
// intentionally left untouched for existing crawlers.
func (h *PublicTransitHandler) SnapshotV2(c *gin.Context) {
	payload, err := h.publicTransitService.SnapshotV2Range(c.Request.Context(), requestBaseURL(c), c.Query("range"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	h.writePublicSnapshot(c, payload)
}

func (h *PublicTransitHandler) writePublicSnapshot(c *gin.Context, payload any) {
	filtered, err := h.publicTransitService.PublicSnapshot(c.Request.Context(), payload)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, filtered)
}

func (h *PublicTransitHandler) GetExportSections(c *gin.Context) {
	sections, err := h.publicTransitService.GetExportSections(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, sections)
}

func (h *PublicTransitHandler) GetExportMetrics(c *gin.Context) {
	response.Success(c, service.PublicTransitMetricCatalog())
}

func (h *PublicTransitHandler) UpdateExportSections(c *gin.Context) {
	var sections service.PublicTransitExportSections
	if err := c.ShouldBindJSON(&sections); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.publicTransitService.SetExportSections(c.Request.Context(), sections); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, sections)
}

func (h *PublicTransitHandler) PreviewExport(c *gin.Context) {
	full, err := h.publicTransitService.SnapshotAuto(c.Request.Context(), requestBaseURL(c), c.Query("range"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	public, err := h.publicTransitService.PublicSnapshot(c.Request.Context(), full)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"full": full, "public": public})
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
