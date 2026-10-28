package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) GetDownstreamAutoRateConfig(c *gin.Context) {
	cfg, err := h.usageService.GetDownstreamAutoRateConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func (h *UsageHandler) SaveDownstreamAutoRateConfig(c *gin.Context) {
	var cfg service.DownstreamAutoRateConfig
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	for _, id := range cfg.GroupIDs {
		if _, err := h.adminService.GetGroup(c.Request.Context(), id); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	if err := h.usageService.SaveDownstreamAutoRateConfig(c.Request.Context(), cfg); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"saved": true})
}

// GetDownstreamAssessment evaluates current group members over an inclusive date range.
// GET /api/v1/admin/usage/downstream-assessment
func (h *UsageHandler) GetDownstreamAssessment(c *gin.Context) {
	groupID, err := strconv.ParseInt(strings.TrimSpace(c.Query("group_id")), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "A positive group_id is required")
		return
	}
	sortBy := strings.TrimSpace(c.DefaultQuery("sort_by", "total_tokens"))
	if sortBy != "total_tokens" && sortBy != "daily_average" {
		response.BadRequest(c, "sort_by must be total_tokens or daily_average")
		return
	}
	sortOrder := strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort_order", "asc")))
	if sortOrder != "asc" && sortOrder != "desc" {
		response.BadRequest(c, "sort_order must be asc or desc")
		return
	}
	threshold, err := strconv.ParseInt(strings.TrimSpace(c.Query("daily_threshold")), 10, 64)
	if err != nil || threshold <= 0 {
		response.BadRequest(c, "daily_threshold is required and must be a positive integer token count")
		return
	}
	loc := timezone.Location()
	if name := strings.TrimSpace(c.Query("timezone")); name != "" {
		loc, err = time.LoadLocation(name)
		if err != nil {
			response.BadRequest(c, "Invalid timezone")
			return
		}
	}
	start, err := time.ParseInLocation("2006-01-02", c.Query("start_date"), loc)
	if err != nil {
		response.BadRequest(c, "start_date is required, use YYYY-MM-DD")
		return
	}
	end, err := time.ParseInLocation("2006-01-02", c.Query("end_date"), loc)
	if err != nil || end.Before(start) {
		response.BadRequest(c, "end_date is required and must be on or after start_date, use YYYY-MM-DD")
		return
	}
	// Count calendar dates in UTC; elapsed local hours can be 23/25 across DST.
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	days := int((endDay.Unix()-startDay.Unix())/86400) + 1
	if _, err := h.adminService.GetGroup(c.Request.Context(), groupID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	page, pageSize := response.ParsePagination(c)
	result, err := h.usageService.GetDownstreamAssessment(c.Request.Context(), pagination.PaginationParams{Page: page, PageSize: pageSize}, service.DownstreamAssessmentFilter{
		GroupID: groupID, StartTime: start, EndTime: end.AddDate(0, 0, 1), Days: days, DailyThreshold: threshold,
		SortBy: sortBy, SortOrder: sortOrder,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
