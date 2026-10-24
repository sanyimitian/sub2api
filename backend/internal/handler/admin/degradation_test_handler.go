package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// DegradationTestHandler handles admin degradation-test-plan management.
type DegradationTestHandler struct {
	degradationTestSvc *service.DegradationTestService
	runner             *service.DegradationTestRunnerService
}

// NewDegradationTestHandler creates a new DegradationTestHandler.
func NewDegradationTestHandler(degradationTestSvc *service.DegradationTestService, runner *service.DegradationTestRunnerService) *DegradationTestHandler {
	return &DegradationTestHandler{degradationTestSvc: degradationTestSvc, runner: runner}
}

type createDegradationTestPlanRequest struct {
	AccountID       int64   `json:"account_id" binding:"required"`
	ModelID         string  `json:"model_id"`
	ReasoningEffort string  `json:"reasoning_effort"`
	Prompt          *string `json:"prompt"`
	CronExpression  string  `json:"cron_expression" binding:"required"`
	Enabled         *bool   `json:"enabled"`
	MaxResults      *int    `json:"max_results"` // Accepted for older clients; retention is fixed at 24.
}

type updateDegradationTestPlanRequest struct {
	ModelID         *string `json:"model_id"`
	ReasoningEffort *string `json:"reasoning_effort"`
	Prompt          *string `json:"prompt"`
	CronExpression  *string `json:"cron_expression"`
	Enabled         *bool   `json:"enabled"`
	MaxResults      *int    `json:"max_results"` // Accepted for older clients; retention is fixed at 24.
}

// ListStatus GET /admin/degradation-test-plans/status
func (h *DegradationTestHandler) ListStatus(c *gin.Context) {
	statuses, err := h.degradationTestSvc.ListStatus(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, statuses)
}

// ListByAccount GET /admin/accounts/:id/degradation-test-plans
func (h *DegradationTestHandler) ListByAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "invalid account id")
		return
	}

	plans, err := h.degradationTestSvc.ListPlansByAccount(c.Request.Context(), accountID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, plans)
}

// Create POST /admin/degradation-test-plans
func (h *DegradationTestHandler) Create(c *gin.Context) {
	var req createDegradationTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	plan := &service.DegradationTestPlan{
		AccountID:       req.AccountID,
		ModelID:         req.ModelID,
		ReasoningEffort: req.ReasoningEffort,
		Prompt:          service.DefaultDegradationTestPrompt,
		CronExpression:  req.CronExpression,
		Enabled:         true,
		MaxResults:      service.DegradationTestMaxResults,
	}
	if req.Prompt != nil {
		plan.Prompt = *req.Prompt
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}

	created, err := h.degradationTestSvc.CreatePlan(c.Request.Context(), plan)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, created)
}

// Update PUT /admin/degradation-test-plans/:id
func (h *DegradationTestHandler) Update(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || planID <= 0 {
		response.BadRequest(c, "invalid plan id")
		return
	}

	existing, err := h.degradationTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}

	var req updateDegradationTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.ModelID != nil {
		existing.ModelID = *req.ModelID
	}
	if req.Prompt != nil {
		existing.Prompt = *req.Prompt
	}
	if req.ReasoningEffort != nil {
		existing.ReasoningEffort = *req.ReasoningEffort
	}
	if req.CronExpression != nil {
		existing.CronExpression = *req.CronExpression
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	updated, err := h.degradationTestSvc.UpdatePlan(c.Request.Context(), existing)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Delete DELETE /admin/degradation-test-plans/:id
func (h *DegradationTestHandler) Delete(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || planID <= 0 {
		response.BadRequest(c, "invalid plan id")
		return
	}

	if err := h.degradationTestSvc.DeletePlan(c.Request.Context(), planID); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ListResults GET /admin/degradation-test-plans/:id/results
func (h *DegradationTestHandler) ListResults(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || planID <= 0 {
		response.BadRequest(c, "invalid plan id")
		return
	}

	limit := service.DegradationTestDefaultResultLimit
	if raw := c.Query("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 {
			response.BadRequest(c, "limit must be a positive integer")
			return
		}
	}

	results, err := h.degradationTestSvc.ListResults(c.Request.Context(), planID, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, results)
}

// RunNow POST /admin/degradation-test-plans/:id/run
func (h *DegradationTestHandler) RunNow(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || planID <= 0 {
		response.BadRequest(c, "invalid plan id")
		return
	}
	result, err := h.runner.RunNow(c.Request.Context(), planID)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			response.NotFound(c, "plan not found")
		case errors.Is(err, service.ErrDegradationTestRunning):
			response.Error(c, http.StatusConflict, err.Error())
		case errors.Is(err, service.ErrDegradationTestCapacity):
			response.Error(c, http.StatusTooManyRequests, err.Error())
		default:
			response.InternalError(c, err.Error())
		}
		return
	}
	c.JSON(http.StatusOK, result)
}
