package middleware

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ChannelMonitorProbeMarker consumes and verifies the internal marker before
// authentication and gateway handlers run. Invalid markers are ignored.
func ChannelMonitorProbeMarker(signer *service.ChannelMonitorProbeSigner) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c == nil || c.Request == nil {
			return
		}
		ctx, _ := service.ConsumeChannelMonitorProbeMarker(c.Request.Context(), c.Request, signer, time.Now().UTC())
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
