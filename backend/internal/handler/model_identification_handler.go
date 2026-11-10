package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

type modelIdentificationService interface {
	AdminConfig(context.Context, int64) (*service.ModelIdentificationAdminConfig, error)
	ListGroups(context.Context) ([]*service.ModelIdentificationGroup, error)
	GetPlan(context.Context, int64) (*service.DegradationTestPlan, error)
	Save(context.Context, int64, service.ModelIdentificationInput) (*service.DegradationTestPlan, error)
	Delete(context.Context, int64) error
	Results(context.Context, int64, bool) ([]*service.DegradationTestResult, error)
}
type modelIdentificationRunner interface {
	RunNow(context.Context, int64) (*service.DegradationTestPlan, error)
}
type ModelIdentificationHandler struct {
	svc    modelIdentificationService
	runner modelIdentificationRunner
}

func NewModelIdentificationHandler(svc *service.ModelIdentificationService, runner *service.DegradationTestRunnerService) *ModelIdentificationHandler {
	return &ModelIdentificationHandler{svc: svc, runner: runner}
}

func (h *ModelIdentificationHandler) Config(c *gin.Context) {
	id, ok := identificationID(c)
	if !ok {
		return
	}
	config, err := h.svc.AdminConfig(c.Request.Context(), id)
	if err != nil {
		h.readError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, config)
}

func (h *ModelIdentificationHandler) List(c *gin.Context) {
	groups, err := h.svc.ListGroups(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法加载模型鉴定分组")
		return
	}
	response.Success(c, groups)
}
func (h *ModelIdentificationHandler) Results(c *gin.Context) {
	id, ok := identificationID(c)
	if !ok {
		return
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	results, err := h.svc.Results(c.Request.Context(), id, role == service.RoleAdmin)
	if err != nil {
		h.readError(c, err)
		return
	}
	response.Success(c, results)
}
func (h *ModelIdentificationHandler) Create(c *gin.Context) { h.save(c, 0) }
func (h *ModelIdentificationHandler) Update(c *gin.Context) {
	id, ok := identificationID(c)
	if !ok {
		return
	}
	h.save(c, id)
}
func (h *ModelIdentificationHandler) save(c *gin.Context, id int64) {
	var input service.ModelIdentificationInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "检测配置格式不正确")
		return
	}
	plan, err := h.svc.Save(c.Request.Context(), id, input)
	if err != nil {
		var pg *pq.Error
		if errors.As(err, &pg) {
			if pg.Code == "23505" {
				response.Error(c, http.StatusConflict, "检测配置发生冲突，请刷新后重试")
				return
			}
			response.InternalError(c, "保存检测计划失败")
			return
		}
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, plan)
}
func (h *ModelIdentificationHandler) Delete(c *gin.Context) {
	id, ok := identificationID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.readError(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
func (h *ModelIdentificationHandler) Run(c *gin.Context) {
	id, ok := identificationID(c)
	if !ok {
		return
	}
	if _, err := h.svc.GetPlan(c.Request.Context(), id); err != nil {
		h.readError(c, err)
		return
	}
	plan, err := h.runner.RunNow(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrDegradationTestRunning) {
			response.Error(c, http.StatusConflict, "该分组正在检测中")
			return
		}
		if errors.Is(err, service.ErrDegradationTestCapacity) || errors.Is(err, service.ErrDegradationTestStopped) {
			response.Error(c, http.StatusServiceUnavailable, "检测服务繁忙，请稍后重试")
			return
		}
		h.readError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"code": 0, "data": plan})
}
func (h *ModelIdentificationHandler) readError(c *gin.Context, err error) {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, service.ErrGroupNotFound) {
		response.NotFound(c, "检测计划不存在")
		return
	}
	response.InternalError(c, "无法读取检测计划")
}
func identificationID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "检测计划编号无效")
		return 0, false
	}
	return id, true
}
