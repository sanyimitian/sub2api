package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type benchmarkGroupTargets interface {
	Groups(context.Context, int64) ([]service.BenchmarkGroupOption, error)
	Models(context.Context, int64, int64) ([]string, error)
	Target(context.Context, int64, int64, string, string) (*service.BenchmarkGroupTarget, error)
}

func (h *ModelBenchmarkHandler) SetGroupService(groups *service.ModelBenchmarkGroupService) {
	h.groups = groups
}

func (h *ModelBenchmarkHandler) localGroupAdmin(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	role, _ := middleware.GetUserRoleFromContext(c)
	if !ok || subject.UserID <= 0 || role != service.RoleAdmin {
		response.Error(c, http.StatusForbidden, "仅管理员可以配置分组测试")
		return 0, false
	}
	source, err := h.connection(c)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "无法读取主站配置")
		return 0, false
	}
	if source.SourceURL != "" {
		response.Error(c, http.StatusForbidden, "请在主站配置分组定时测试")
		return 0, false
	}
	if h.groups == nil {
		response.Error(c, http.StatusServiceUnavailable, "分组测试服务未配置")
		return 0, false
	}
	return subject.UserID, true
}

func (h *ModelBenchmarkHandler) Groups(c *gin.Context) {
	userID, ok := h.localGroupAdmin(c)
	if !ok {
		return
	}
	items, err := h.groups.Groups(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

func (h *ModelBenchmarkHandler) GroupModels(c *gin.Context) {
	userID, ok := h.localGroupAdmin(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("group_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "请选择有效的分组")
		return
	}
	models, err := h.groups.Models(c.Request.Context(), userID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"models": models})
}

// No browser-supplied destination, credential or display name can override the
// selected group. Only the resolved server target is sent to the worker.
func (h *ModelBenchmarkHandler) prepareGroupSchedule(c *gin.Context) bool {
	userID, ok := h.localGroupAdmin(c)
	if !ok {
		return false
	}
	var input struct {
		GroupID int64  `json:"group_id"`
		Model   string `json:"model"`
		Effort  string `json:"effort"`
		Name    string `json:"name"`
		Cron    string `json:"cron_expression"`
		Enabled bool   `json:"enabled"`
		Public  bool   `json:"public_results"`
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 16*1024+1))
	if err != nil || len(raw) > 16*1024 {
		response.BadRequest(c, "请求过大")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.GroupID <= 0 || !benchmarkPublicText(input.Name, 100) || strings.TrimSpace(input.Name) == "" || !benchmarkPublicText(input.Cron, 100) || !benchmarkPublicText(input.Model, 200) {
		response.BadRequest(c, "请选择分组、模型并填写有效的定时任务配置")
		return false
	}
	if input.Effort == "" {
		input.Effort = "medium"
	}
	target, err := h.groups.Target(c.Request.Context(), userID, input.GroupID, input.Model, input.Effort)
	if err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	data := struct {
		*service.BenchmarkGroupTarget
		Name    string `json:"name"`
		Cron    string `json:"cron_expression"`
		Enabled bool   `json:"enabled"`
		Public  bool   `json:"public_results"`
	}{target, input.Name, input.Cron, input.Enabled, input.Public}
	body, err := json.Marshal(data)
	if err != nil {
		response.InternalError(c, "无法创建分组测试")
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	return true
}
