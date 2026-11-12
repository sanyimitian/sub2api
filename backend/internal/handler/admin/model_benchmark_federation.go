package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

var benchmarkFederatedUserPath = regexp.MustCompile(`^/users/[1-9][0-9]{0,18}/(?:state|results|runs(?:/[a-f0-9]{32}(?:/(?:rerun|cancel))?)?)$`)
var benchmarkFederatedReadPath = regexp.MustCompile(`^/(?:state|showcase|results|user-runs|runs/[a-f0-9]{32})$`)
var benchmarkFederatedWritePath = regexp.MustCompile(`^/users/[1-9][0-9]{0,18}/runs(?:/[a-f0-9]{32}/(?:rerun|cancel))?$`)

func (h *ModelBenchmarkHandler) connection(c *gin.Context) (*service.ModelBenchmarkConnection, error) {
	const key = "model_benchmark_connection"
	if value, ok := c.Get(key); ok {
		return value.(*service.ModelBenchmarkConnection), nil
	}
	connection, err := h.federation.Connection(c.Request.Context())
	if err == nil {
		c.Set(key, connection)
	}
	return connection, err
}

func allowedBenchmarkFederatedRequest(method, path string) bool {
	if method == http.MethodGet {
		return (benchmarkFederatedUserPath.MatchString(path) && !strings.HasSuffix(path, "/cancel") && !strings.HasSuffix(path, "/rerun")) || benchmarkFederatedReadPath.MatchString(path)
	}
	return method == http.MethodPost && benchmarkFederatedWritePath.MatchString(path)
}

func (h *ModelBenchmarkHandler) Source(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.federation == nil {
		response.Error(c, 503, "主站接入服务不可用")
		return
	}
	if c.Request.Method == http.MethodGet {
		config, err := h.federation.Config(c.Request.Context())
		if err != nil {
			response.Error(c, 503, "无法读取主站配置")
			return
		}
		response.Success(c, config)
		return
	}
	var input struct {
		SourceURL string `json:"source_url"`
		Token     string `json:"token"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "主站配置格式不正确")
		return
	}
	config, err := h.federation.SaveSource(c.Request.Context(), input.SourceURL, input.Token)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, config)
}

func (h *ModelBenchmarkHandler) Sites(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.federation == nil {
		response.Error(c, 503, "站点接入服务不可用")
		return
	}
	config, err := h.federation.Config(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "无法读取主站配置")
		return
	}
	if config.SourceURL != "" {
		response.Error(c, 409, "请在实际执行测试的主站管理接入站点")
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := h.federation.Sites(c.Request.Context())
		if err != nil {
			response.Error(c, 503, "无法读取接入站点")
			return
		}
		response.Success(c, gin.H{"items": items})
	} else if c.Request.Method == http.MethodDelete {
		if err := h.federation.RevokeSite(c.Request.Context(), c.Param("id")); err != nil {
			response.BadRequest(c, "无法撤销接入站点")
			return
		}
		response.Success(c, gin.H{"revoked": true})
	} else {
		var input struct {
			Name string `json:"name"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&input) != nil {
			response.BadRequest(c, "请填写站点名称")
			return
		}
		site, token, err := h.federation.CreateSite(c.Request.Context(), input.Name)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		response.Success(c, gin.H{"site": site, "token": token})
	}
}

// Federation 接受已授权站点的请求，不使用浏览器身份或任意站点头。
func (h *ModelBenchmarkHandler) Federation(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	site, err := h.federation.Authenticate(c.Request.Context(), token)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "站点认证失败")
		return
	}
	config, err := h.connection(c)
	if err != nil {
		response.Error(c, 503, "无法读取主站配置")
		return
	}
	if config.SourceURL != "" {
		response.Error(c, 409, "请直接连接实际执行测试的主站")
		return
	}
	path := c.Param("path")
	if path == "/info" && c.Request.Method == http.MethodGet {
		response.Success(c, gin.H{"version": 1, "site_id": site.ID})
		return
	}
	if !allowedBenchmarkFederatedRequest(c.Request.Method, path) {
		response.Error(c, 403, "站点无权执行此操作")
		return
	}
	if c.Request.URL.RawQuery != "" {
		path += "?" + c.Request.URL.RawQuery
	}
	h.forward(c, "/sites/"+site.ID+path)
}

func (h *ModelBenchmarkHandler) forwardRemote(c *gin.Context, connection *service.ModelBenchmarkConnection, path string, transform func([]byte) ([]byte, error)) {
	u, err := url.Parse(path)
	if err != nil || !allowedBenchmarkFederatedRequest(c.Request.Method, u.Path) {
		response.Error(c, http.StatusForbidden, "测试配置由主站统一管理，请前往主站操作")
		return
	}
	var body []byte
	if c.Request.Body != nil {
		body, err = io.ReadAll(io.LimitReader(c.Request.Body, 16*1024+1))
		if err != nil || len(body) > 16*1024 {
			response.BadRequest(c, "请求内容过大")
			return
		}
	}
	res, err := connection.RemoteRequest(c.Request.Context(), c.Request.Method, path, bytes.NewReader(body))
	if err != nil {
		response.Error(c, 503, "主站暂时不可用，请稍后重试")
		return
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
	var envelope struct {
		Code    *int            `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err != nil || len(data) > 8*1024*1024 || json.Unmarshal(data, &envelope) != nil || envelope.Code == nil {
		response.Error(c, 502, "主站响应格式不正确")
		return
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || *envelope.Code != 0 {
		status := res.StatusCode
		if status == 401 || status == 403 {
			response.Error(c, 503, "主站拒绝请求，请检查站点接入密钥或主站的用户测试开关")
			return
		}
		if status < 400 || status > 599 {
			status = 502
		}
		message := envelope.Message
		if message == "" {
			message = "主站请求失败"
		}
		response.Error(c, status, message)
		return
	}
	if len(envelope.Data) == 0 {
		response.Error(c, 502, "主站响应缺少数据")
		return
	}
	if transform != nil {
		envelope.Data, err = transform(envelope.Data)
		if err != nil {
			response.Error(c, 503, "无法加载用户测试记录")
			return
		}
	}
	c.JSON(res.StatusCode, response.Response{Code: 0, Message: "success", Data: envelope.Data})
}
