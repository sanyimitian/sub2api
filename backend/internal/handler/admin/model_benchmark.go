package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ModelBenchmarkHandler forwards administrator requests to the isolated evaluator.
// The worker URL and credential are deployment settings, never browser inputs.
type ModelBenchmarkHandler struct {
	baseURL     string
	token       string
	client      *http.Client
	publicGuard *benchmarkPublicGuard
	users       benchmarkUsers
	federation  *service.ModelBenchmarkFederationService
}

type benchmarkUsers interface {
	ListUsers(context.Context, int, int, service.UserListFilters, string, string) ([]service.User, int64, error)
	GetUserIncludeDeleted(context.Context, int64) (*service.User, error)
}

// Account access is wired only for the administrator handler.
func NewModelBenchmarkHandlerForAdmin(users *UserHandler, federation *service.ModelBenchmarkFederationService) *ModelBenchmarkHandler {
	h := NewModelBenchmarkHandler()
	h.federation = federation
	if users != nil {
		h.users = users.adminService
	}
	return h
}

type benchmarkOwner struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Deleted  bool   `json:"deleted"`
	Missing  bool   `json:"missing"`
	SiteID   string `json:"site_id,omitempty"`
}

func (h *ModelBenchmarkHandler) UserOptions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	keyword := strings.TrimSpace(c.Query("q"))
	if len(keyword) > 200 {
		response.BadRequest(c, "Account search is too long")
		return
	}
	items := make([]benchmarkOwner, 0)
	if keyword == "" {
		response.Success(c, gin.H{"items": items, "has_more": false})
		return
	}
	if h.users == nil {
		response.Error(c, http.StatusServiceUnavailable, "Account search is unavailable")
		return
	}
	includeSubscriptions := false
	users, total, err := h.users.ListUsers(c.Request.Context(), 1, 30, service.UserListFilters{
		AccountSearch: keyword, IncludeDeleted: true, IncludeSubscriptions: &includeSubscriptions,
	}, "email", "asc")
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Account search is unavailable")
		return
	}
	for _, user := range users {
		items = append(items, benchmarkOwner{ID: user.ID, Email: user.Email, Username: user.Username, Deleted: user.DeletedAt != nil})
	}
	response.Success(c, gin.H{"items": items, "has_more": total > int64(len(items))})
}

func (h *ModelBenchmarkHandler) UserRuns(c *gin.Context) {
	source, sourceErr := h.connection(c)
	if sourceErr != nil {
		response.Error(c, http.StatusServiceUnavailable, "无法读取主站配置")
		return
	}
	query := url.Values{}
	for key, values := range c.Request.URL.Query() {
		// 前端统一附加的时区只影响本地显示，不作为评测筛选条件转发。
		if (key != "page" && key != "user_id" && key != "model" && key != "timezone") || len(values) != 1 {
			response.BadRequest(c, "Invalid benchmark filters")
			return
		}
	}
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 || page > 1000000 || len(c.Query("model")) > 800 {
		response.BadRequest(c, "Invalid benchmark filters")
		return
	}
	query.Set("page", strconv.Itoa(page))
	if raw := c.Query("user_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid user ID")
			return
		}
		query.Set("user_id", strconv.FormatInt(id, 10))
	}
	query.Set("model", strings.TrimSpace(c.Query("model")))
	h.forwardWith(c, "/user-runs?"+query.Encode(), func(data []byte) ([]byte, error) {
		var list struct {
			Items    []map[string]json.RawMessage `json:"items"`
			Total    int                          `json:"total"`
			Page     int                          `json:"page"`
			PageSize int                          `json:"page_size"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, err
		}
		if len(list.Items) > 20 {
			return nil, errors.New("invalid page size")
		}
		owners := make(map[int64]benchmarkOwner)
		for _, item := range list.Items {
			var id int64
			if err := json.Unmarshal(item["owner_user_id"], &id); err != nil || id <= 0 {
				return nil, errors.New("invalid benchmark owner")
			}
			var siteID string
			if raw, ok := item["owner_site_id"]; ok {
				if err := json.Unmarshal(raw, &siteID); err != nil {
					return nil, err
				}
			}
			// 外站账号编号不能用于查询本站的同编号账号。
			if siteID != source.SiteID {
				item["owner"], _ = json.Marshal(benchmarkOwner{ID: id, SiteID: siteID, Missing: true})
				continue
			}
			owner, ok := owners[id]
			if !ok {
				if h.users == nil {
					return nil, errors.New("account lookup unavailable")
				}
				owner = benchmarkOwner{ID: id, Missing: true}
				user, err := h.users.GetUserIncludeDeleted(c.Request.Context(), id)
				if err != nil && !errors.Is(err, service.ErrUserNotFound) {
					return nil, err
				}
				if user != nil {
					owner = benchmarkOwner{ID: id, Email: user.Email, Username: user.Username, Deleted: user.DeletedAt != nil}
				}
				owners[id] = owner
			}
			item["owner"], _ = json.Marshal(owner)
		}
		if list.Items == nil {
			list.Items = make([]map[string]json.RawMessage, 0)
		}
		return json.Marshal(list)
	})
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
		h.Showcase(c)
		return
	}
	if path == "/results" {
		role, _ := middleware.GetUserRoleFromContext(c)
		if role == service.RoleAdmin {
			h.Results(c)
		} else {
			h.forward(c, "/users/"+strconv.FormatInt(subject.UserID, 10)+"/results?"+benchmarkResultQuery(c))
		}
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

func benchmarkResultQuery(c *gin.Context) string {
	query := url.Values{}
	query.Set("cursor", c.Query("cursor"))
	return query.Encode()
}

func (h *ModelBenchmarkHandler) Showcase(c *gin.Context) {
	h.forward(c, "/showcase?"+benchmarkResultQuery(c))
}

func (h *ModelBenchmarkHandler) Results(c *gin.Context) {
	h.forward(c, "/results?"+benchmarkResultQuery(c))
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
func (h *ModelBenchmarkHandler) Delete(c *gin.Context)    { h.runAction(c, "") }
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
	h.forwardWith(c, path, nil)
}

func (h *ModelBenchmarkHandler) forwardWith(c *gin.Context, path string, transform func([]byte) ([]byte, error)) {
	c.Header("Cache-Control", "no-store")
	source, sourceErr := h.connection(c)
	if sourceErr != nil {
		response.Error(c, http.StatusServiceUnavailable, "无法读取主站配置")
		return
	}
	if source.SourceURL != "" {
		h.forwardRemote(c, source, path, transform)
		return
	}
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
		if transform != nil {
			data, err = transform(data)
			if err != nil {
				response.Error(c, http.StatusServiceUnavailable, "Cannot load benchmark user records")
				return
			}
		}
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
