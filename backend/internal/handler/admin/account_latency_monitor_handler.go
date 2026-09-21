package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountLatencyMonitorHandler struct {
	monitor *service.AccountLatencyMonitor
}

type updateAccountLatencyMonitorAnomalyBaseRequest struct {
	AnomalyBase *int `json:"anomaly_base"`
}

func NewAccountLatencyMonitorHandler(monitor *service.AccountLatencyMonitor) *AccountLatencyMonitorHandler {
	return &AccountLatencyMonitorHandler{monitor: monitor}
}

func (h *AccountLatencyMonitorHandler) GetSettings(c *gin.Context) {
	settings, err := h.monitor.GetSettings(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, settings)
}

func (h *AccountLatencyMonitorHandler) UpdateSettings(c *gin.Context) {
	var settings service.AccountLatencyMonitorSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.monitor.UpdateSettings(c.Request.Context(), &settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, settings)
}

func (h *AccountLatencyMonitorHandler) GetRuntime(c *gin.Context) {
	runtime, err := h.monitor.GetRuntime(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": runtime})
}

func (h *AccountLatencyMonitorHandler) ProbeGroup(c *gin.Context) {
	groupID, ok := accountLatencyMonitorGroupID(c)
	if !ok {
		return
	}
	if err := h.monitor.StartProbeGroup(c.Request.Context(), groupID); err != nil {
		accountLatencyMonitorOperationError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "账号探测已开始"})
}

func (h *AccountLatencyMonitorHandler) ActivateBestAccounts(c *gin.Context) {
	groupID, ok := accountLatencyMonitorGroupID(c)
	if !ok {
		return
	}
	if err := h.monitor.ActivateBestAccounts(c.Request.Context(), groupID); err != nil {
		accountLatencyMonitorOperationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已按最近一次探测结果更换最优账号"})
}

func (h *AccountLatencyMonitorHandler) UpdateAccountAnomalyBase(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("accountID"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "账号编号无效")
		return
	}
	var req updateAccountLatencyMonitorAnomalyBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.AnomalyBase == nil {
		response.BadRequest(c, "异常值基数不能为空")
		return
	}
	if *req.AnomalyBase < 0 || *req.AnomalyBase > service.AccountLatencyMonitorMaxAnomalyBase {
		response.BadRequest(c, "异常值基数必须在 0 到 2147483647 之间")
		return
	}
	if err := h.monitor.UpdateAccountAnomalyBase(c.Request.Context(), accountID, *req.AnomalyBase); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"anomaly_base": *req.AnomalyBase})
}

func accountLatencyMonitorGroupID(c *gin.Context) (int64, bool) {
	groupID, err := strconv.ParseInt(c.Param("groupID"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "分组编号无效")
		return 0, false
	}
	return groupID, true
}

func accountLatencyMonitorOperationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAccountLatencyMonitorGroupDisabled),
		errors.Is(err, service.ErrAccountLatencyMonitorBusy),
		errors.Is(err, service.ErrAccountLatencyMonitorNoProbeResult):
		response.BadRequest(c, err.Error())
	default:
		response.InternalError(c, err.Error())
	}
}
