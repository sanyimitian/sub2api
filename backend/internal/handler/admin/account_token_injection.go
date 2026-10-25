package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// TestTokenInjection is an admin-only diagnostic; it deliberately does not run
// the connectivity test's account-recovery side effects.
func (h *AccountHandler) TestTokenInjection(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req struct {
		ModelID  string `json:"model_id" binding:"required,max=200"`
		Protocol string `json:"protocol" binding:"omitempty,oneof=auto responses chat"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.ModelID) == "" {
		response.BadRequest(c, "A text model and valid test protocol are required")
		return
	}
	_ = h.accountTestService.TestTokenInjection(c, id, strings.TrimSpace(req.ModelID), req.Protocol)
}
