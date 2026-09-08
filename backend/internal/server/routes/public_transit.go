package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

func RegisterPublicTransitRoutes(r *gin.Engine, v1 *gin.RouterGroup, h *handler.Handlers) {
	if h == nil || h.PublicTransit == nil {
		return
	}
	r.GET(service.PublicTransitWellKnownPath, h.PublicTransit.Discovery)
	r.GET(service.PublicTransitSnapshotPath, h.PublicTransit.SnapshotAuto)
	r.GET(service.PublicTransitV1SnapshotPath, h.PublicTransit.Snapshot)
	r.GET(service.PublicTransitPassiveSnapshotPath, h.PublicTransit.SnapshotV2)
	v1.GET("/public/transit/snapshot", h.PublicTransit.Snapshot)
	v1.GET("/public/transit/v2/snapshot", h.PublicTransit.SnapshotV2)
}
