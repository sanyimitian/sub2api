package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// ModelBenchmarkHandler forwards administrator requests to the isolated evaluator.
// The worker URL and credential are deployment settings, never browser inputs.
type ModelBenchmarkHandler struct {
	baseURL     string
	token       string
	client      *http.Client
	publicGuard *benchmarkPublicGuard
}

func NewModelBenchmarkHandler() *ModelBenchmarkHandler {
	return &ModelBenchmarkHandler{
		publicGuard: newBenchmarkPublicGuard(),
		baseURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("MODEL_BENCHMARK_WORKER_URL")), "/"),
		token:       strings.TrimSpace(os.Getenv("MODEL_BENCHMARK_WORKER_TOKEN")),
		client: &http.Client{
			Timeout: 25 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

var benchmarkID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// User forwards only an authenticated subject's namespace. Browser-supplied
// owner IDs, role hints and worker credentials cannot change this scope.
func (h *ModelBenchmarkHandler) User(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Error(c, http.StatusUnauthorized, "Authentication required")
		return
	}
	path := strings.TrimPrefix(c.FullPath(), "/api/v1/model-benchmarks")
	if path == "/showcase" {
		h.forward(c, path)
		return
	}
	if id := c.Param("id"); id != "" {
		if !benchmarkID.MatchString(id) {
			response.BadRequest(c, "Invalid benchmark ID")
			return
		}
		path = strings.Replace(path, ":id", id, 1)
	}
	if path == "/runs" && c.Request.Method == http.MethodGet {
		query := url.Values{}
		query.Set("page", c.DefaultQuery("page", "1"))
		path += "?" + query.Encode()
	}
	h.forward(c, "/users/"+strconv.FormatInt(subject.UserID, 10)+path)
}

func (h *ModelBenchmarkHandler) State(c *gin.Context)        { h.forward(c, "/state") }
func (h *ModelBenchmarkHandler) UpdateBank(c *gin.Context)   { h.forward(c, "/bank/update") }
func (h *ModelBenchmarkHandler) ActivateBank(c *gin.Context) { h.forward(c, "/bank/activate") }
func (h *ModelBenchmarkHandler) PrepareEnvironment(c *gin.Context) {
	h.forward(c, "/environment/retry")
}
func (h *ModelBenchmarkHandler) Execution(c *gin.Context) { h.forward(c, "/execution") }
func (h *ModelBenchmarkHandler) Displays(c *gin.Context)  { h.forward(c, "/displays") }
func (h *ModelBenchmarkHandler) Display(c *gin.Context) {
	if !benchmarkID.MatchString(c.Param("id")) {
		response.BadRequest(c, "Invalid display ID")
		return
	}
	path := "/displays/" + c.Param("id")
	if strings.HasSuffix(c.FullPath(), "/refresh") {
		path += "/refresh"
	}
	h.forward(c, path)
}
func (h *ModelBenchmarkHandler) Config(c *gin.Context)  { h.forward(c, "/config") }
func (h *ModelBenchmarkHandler) Refresh(c *gin.Context) { h.forward(c, "/suites/refresh") }
func (h *ModelBenchmarkHandler) Create(c *gin.Context)  { h.forward(c, "/runs") }
func (h *ModelBenchmarkHandler) List(c *gin.Context) {
	query := url.Values{}
	query.Set("page", c.DefaultQuery("page", "1"))
	h.forward(c, "/runs?"+query.Encode())
}
func (h *ModelBenchmarkHandler) Detail(c *gin.Context)    { h.runAction(c, "") }
func (h *ModelBenchmarkHandler) Rerun(c *gin.Context)     { h.runAction(c, "/rerun") }
func (h *ModelBenchmarkHandler) Cancel(c *gin.Context)    { h.runAction(c, "/cancel") }
func (h *ModelBenchmarkHandler) Schedules(c *gin.Context) { h.forward(c, "/schedules") }
func (h *ModelBenchmarkHandler) Schedule(c *gin.Context) {
	if !benchmarkID.MatchString(c.Param("id")) {
		response.BadRequest(c, "Invalid benchmark schedule ID")
		return
	}
	path := "/schedules/" + c.Param("id")
	if strings.HasSuffix(c.FullPath(), "/run") {
		path += "/run"
	}
	h.forward(c, path)
}

func (h *ModelBenchmarkHandler) runAction(c *gin.Context, suffix string) {
	if !benchmarkID.MatchString(c.Param("id")) {
		response.BadRequest(c, "Invalid benchmark ID")
		return
	}
	h.forward(c, "/runs/"+c.Param("id")+suffix)
}

func (h *ModelBenchmarkHandler) forward(c *gin.Context, path string) {
	c.Header("Cache-Control", "no-store")
	u, err := url.Parse(h.baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || h.token == "" {
		response.Error(c, http.StatusServiceUnavailable, "Model benchmark worker is not configured")
		return
	}
	var body []byte
	if c.Request.Body != nil {
		body, err = io.ReadAll(io.LimitReader(c.Request.Body, 16*1024+1))
		if err != nil || len(body) > 16*1024 {
			response.BadRequest(c, "Benchmark request is too large")
			return
		}
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, h.baseURL+path, bytes.NewReader(body))
	if err != nil {
		response.InternalError(c, "Cannot create benchmark request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client.Do(req)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Model benchmark worker is unavailable")
		return
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 || !json.Valid(data) {
		response.Error(c, http.StatusBadGateway, "Invalid benchmark worker response")
		return
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		c.JSON(res.StatusCode, response.Response{Code: 0, Message: "success", Data: json.RawMessage(data)})
		return
	}
	// Only the worker's curated message is exposed; never proxy exception bodies.
	var problem struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &problem)
	status := res.StatusCode
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		// Worker credentials are deployment settings, unrelated to the user's
		// login. Never turn a worker token mismatch into a browser logout.
		response.Error(c, http.StatusServiceUnavailable, "Model benchmark worker authentication failed")
		return
	}
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	if problem.Message == "" {
		problem.Message = "Benchmark request failed"
	}
	response.Error(c, status, problem.Message)
}
