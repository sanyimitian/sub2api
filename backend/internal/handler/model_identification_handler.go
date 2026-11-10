package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

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
	source *service.ModelIdentificationSourceService
}

func NewModelIdentificationHandler(svc *service.ModelIdentificationService, runner *service.DegradationTestRunnerService, source *service.ModelIdentificationSourceService) *ModelIdentificationHandler {
	return &ModelIdentificationHandler{svc: svc, runner: runner, source: source}
}

func (h *ModelIdentificationHandler) Config(c *gin.Context) {
	if !h.requireLocal(c) {
		return
	}
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
	c.Header(service.ModelIdentificationInstanceHeader, h.source.InstanceID())
	source, ok := h.readSource(c)
	if !ok {
		return
	}
	var groups []*service.ModelIdentificationGroup
	var err error
	if source.ReadOnly {
		groups, err = h.source.Groups(c.Request.Context(), source.SourceURL, true)
	} else {
		groups, err = h.svc.ListGroups(c.Request.Context())
	}
	if err != nil {
		response.InternalError(c, "无法加载模型鉴定分组")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, groups)
}
func (h *ModelIdentificationHandler) Results(c *gin.Context) {
	id, ok := identificationID(c)
	if !ok {
		return
	}
	source, ok := h.readSource(c)
	if !ok {
		return
	}
	var results []*service.DegradationTestResult
	var err error
	if source.ReadOnly {
		results, err = h.source.Results(c.Request.Context(), source.SourceURL, id)
	} else {
		results, err = h.svc.Results(c.Request.Context(), id, false)
	}
	if err != nil {
		h.readError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
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
	if !h.requireLocal(c) {
		return
	}
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
	if !h.requireLocal(c) {
		return
	}
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
	if !h.requireLocal(c) {
		return
	}
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

// 公开只读接口始终脱敏，即使请求携带管理员身份也不返回私有错误。
func (h *ModelIdentificationHandler) Source(c *gin.Context) {
	source, ok := h.readSource(c)
	if ok {
		c.Header("Cache-Control", "no-store")
		response.Success(c, source)
	}
}
func (h *ModelIdentificationHandler) SaveSource(c *gin.Context) {
	var input struct {
		SourceURL string `json:"source_url"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "来源配置格式不正确")
		return
	}
	if u, err := url.Parse(strings.TrimSpace(input.SourceURL)); err == nil && u.Host != "" && strings.EqualFold(u.Host, c.Request.Host) {
		response.BadRequest(c, "不能将本站设置为自己的鉴定来源")
		return
	}
	source, err := h.source.Save(c.Request.Context(), input.SourceURL)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, source)
}
func (h *ModelIdentificationHandler) readSource(c *gin.Context) (*service.ModelIdentificationSource, bool) {
	source, err := h.source.Config(c.Request.Context())
	if err != nil {
		response.InternalError(c, "无法读取鉴定来源配置")
		return nil, false
	}
	if source.ReadOnly && c.GetHeader(service.ModelIdentificationProxyHeader) != "" {
		response.Error(c, 409, "请直接使用执行检测的主站作为来源，不能串联结果站点")
		return nil, false
	}
	return source, true
}
func (h *ModelIdentificationHandler) requireLocal(c *gin.Context) bool {
	if err := h.source.RequireLocal(c.Request.Context()); err != nil {
		if errors.Is(err, service.ErrModelIdentificationReadOnly) {
			response.Error(c, 409, err.Error())
		} else {
			response.InternalError(c, "无法读取鉴定来源配置")
		}
		return false
	}
	return true
}
func (h *ModelIdentificationHandler) AdminResults(c *gin.Context) {
	if !h.requireLocal(c) {
		return
	}
	id, ok := identificationID(c)
	if !ok {
		return
	}
	results, err := h.svc.Results(c.Request.Context(), id, true)
	if err != nil {
		h.readError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, results)
}
