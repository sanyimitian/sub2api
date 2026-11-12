//go:build unit

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelBenchmarkAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var hits atomic.Int32
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"ready":true}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-test-token")
	users := &identificationAuthUsers{users: map[int64]*service.User{
		1: {ID: 1, Email: "admin@example.invalid", Role: service.RoleAdmin, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true},
		2: {ID: 2, Email: "user@example.invalid", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true},
	}}
	auth := service.NewAuthService(nil, users, nil, nil, &config.Config{JWT: config.JWTConfig{Secret: "benchmark-auth-test-secret", ExpireHour: 1}}, nil, nil, nil, nil, nil, nil, nil, nil)
	adminToken, err := auth.GenerateToken(context.Background(), users.users[1])
	require.NoError(t, err)
	userToken, err := auth.GenerateToken(context.Background(), users.users[2])
	require.NoError(t, err)
	h := &handler.Handlers{Admin: &handler.AdminHandlers{ModelBenchmark: admin.NewModelBenchmarkHandler()}}
	router := gin.New()
	passAudit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	passStep := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), h, middleware.NewAdminAuthMiddleware(auth, service.NewUserService(users, nil, nil, nil), nil, nil), passAudit, passStep, nil, nil)
	for _, endpoint := range []struct{ method, path string }{
		{"POST", "/environment/retry"},
		{"GET", "/results"}, {"GET", "/showcase"}, {"GET", "/state"}, {"PUT", "/config"}, {"POST", "/suites/refresh"}, {"GET", "/runs"}, {"POST", "/runs"},
		{"GET", "/user-runs"}, {"GET", "/user-options"},
		{"GET", "/runs/" + strings.Repeat("a", 32)}, {"POST", "/runs/" + strings.Repeat("a", 32) + "/rerun"}, {"POST", "/runs/" + strings.Repeat("a", 32) + "/cancel"},
		{"GET", "/schedules"}, {"POST", "/schedules"}, {"PUT", "/schedules/" + strings.Repeat("a", 32)}, {"DELETE", "/schedules/" + strings.Repeat("a", 32)},
		{"POST", "/bank/update"}, {"POST", "/bank/activate"},
		{"POST", "/schedules/" + strings.Repeat("a", 32) + "/run"},
		{"GET", "/execution"}, {"PUT", "/execution"}, {"GET", "/displays"}, {"POST", "/displays"},
		{"PUT", "/displays/" + strings.Repeat("a", 32)}, {"DELETE", "/displays/" + strings.Repeat("a", 32)}, {"POST", "/displays/" + strings.Repeat("a", 32) + "/refresh"},
	} {
		for _, identity := range []struct {
			token string
			want  int
		}{{"", 401}, {userToken, 403}, {adminToken, 200}} {
			before := hits.Load()
			req := httptest.NewRequest(endpoint.method, "/api/v1/admin/model-benchmarks"+endpoint.path, strings.NewReader("{}"))
			req.Header.Set("X-Admin", "true") // Client-provided role hints must not grant access.
			if identity.token != "" {
				req.Header.Set("Authorization", "Bearer "+identity.token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, identity.want, w.Code, "%s %s: %s", endpoint.method, endpoint.path, w.Body)
			if identity.want != 200 {
				require.Equal(t, before, hits.Load(), "unauthorized request reached worker")
			}
		}
	}
	for _, endpoint := range []struct{ method, path string }{
		{"GET", "/source"}, {"PUT", "/source"}, {"GET", "/sites"}, {"POST", "/sites"}, {"DELETE", "/sites/" + strings.Repeat("a", 32)},
	} {
		for _, identity := range []struct {
			token  string
			status int
		}{{"", 401}, {userToken, 403}} {
			req := httptest.NewRequest(endpoint.method, "/api/v1/admin/model-benchmarks"+endpoint.path, strings.NewReader("{}"))
			if identity.token != "" {
				req.Header.Set("Authorization", "Bearer "+identity.token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, identity.status, w.Code, endpoint.path)
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/model-benchmarks/state", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestModelBenchmarkUserAuthenticationAndScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotPath string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		require.Equal(t, "Bearer worker-test-token", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("X-User-ID"))
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-test-token")
	users := &identificationAuthUsers{users: map[int64]*service.User{
		7: {ID: 7, Email: "user@example.invalid", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true},
		1: {ID: 1, Email: "admin@example.invalid", Role: service.RoleAdmin, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true},
	}}
	auth := service.NewAuthService(nil, users, nil, nil, &config.Config{JWT: config.JWTConfig{Secret: "benchmark-auth-test-secret", ExpireHour: 1}}, nil, nil, nil, nil, nil, nil, nil, nil)
	token, err := auth.GenerateToken(context.Background(), users.users[7])
	require.NoError(t, err)
	router := gin.New()
	// Register the same route helper and real JWT middleware used by RegisterUserRoutes.
	group := router.Group("/api/v1")
	group.Use(gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, service.NewUserService(users, nil, nil, nil), nil, nil)))
	registerUserModelBenchmarkRoutes(group)
	adminToken, err := auth.GenerateToken(context.Background(), users.users[1])
	require.NoError(t, err)
	adminRequest := httptest.NewRequest("GET", "/api/v1/model-benchmarks/results?cursor=next", nil)
	adminRequest.Header.Set("Authorization", "Bearer "+adminToken)
	adminResponse := httptest.NewRecorder()
	router.ServeHTTP(adminResponse, adminRequest)
	require.Equal(t, 200, adminResponse.Code)
	require.Equal(t, "/results?cursor=next", gotPath)
	id := strings.Repeat("a", 32)
	for _, endpoint := range []struct{ method, path, workerPath string }{
		{"GET", "/state", "/users/7/state"}, {"GET", "/showcase", "/showcase?cursor="},
		{"GET", "/results?cursor=next&owner_user_id=8&role=admin", "/users/7/results?cursor=next"},
		{"GET", "/runs?page=2&owner_user_id=8", "/users/7/runs?page=2"}, {"POST", "/runs", "/users/7/runs"},
		{"GET", "/runs/" + id, "/users/7/runs/" + id},
		{"POST", "/runs/" + id + "/rerun", "/users/7/runs/" + id + "/rerun"},
		{"POST", "/runs/" + id + "/cancel", "/users/7/runs/" + id + "/cancel"},
	} {
		for _, signedIn := range []bool{false, true} {
			gotPath = ""
			req := httptest.NewRequest(endpoint.method, "/api/v1/model-benchmarks"+endpoint.path, strings.NewReader(`{"owner_user_id":8}`))
			req.Header.Set("X-User-ID", "8")
			if signedIn {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if signedIn {
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Equal(t, endpoint.workerPath, gotPath)
			} else {
				require.Equal(t, 401, w.Code)
				require.Empty(t, gotPath)
			}
		}
	}
	for _, path := range []string{"/schedules", "/schedules/" + id + "/run", "/config", "/suites/refresh", "/execution", "/displays", "/environment/retry", "/bank/update", "/bank/activate"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/model-benchmarks"+path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(w, req)
		require.Equal(t, 404, w.Code)
	}
}

func TestModelBenchmarkPublicFeedAllowsAnonymousCrossOriginReadOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var hits atomic.Int32
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		require.Equal(t, "/public-results", r.URL.RequestURI())
		require.Equal(t, "Bearer worker-test-token", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		_, _ = w.Write([]byte(`{"version":2,"generated_at":"2026-11-12T00:00:00Z","limit":20,"items":[]}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-test-token")
	router := gin.New()
	router.Use(middleware.CORS(config.CORSConfig{AllowedOrigins: []string{"https://own.example"}, AllowCredentials: true}))
	registerPublicModelBenchmarkRoutes(router.Group("/api/v1"), nil)
	for _, method := range []string{"GET", "OPTIONS", "POST"} {
		req := httptest.NewRequest(method, "/api/v1/model-benchmarks/public-results", nil)
		req.Header.Set("Origin", "https://another.example")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if method == "POST" {
			require.Equal(t, 404, w.Code)
			require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		} else {
			require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
			require.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
			if method == "GET" {
				require.Equal(t, 200, w.Code)
				require.JSONEq(t, `{"code":0,"message":"success","data":{"version":2,"generated_at":"2026-11-12T00:00:00Z","limit":20,"items":[]}}`, w.Body.String())
			} else {
				require.Equal(t, 204, w.Code)
			}
		}
	}
	require.Equal(t, int32(1), hits.Load())
	req := httptest.NewRequest("OPTIONS", "/api/v1/admin/model-benchmarks/displays", nil)
	req.Header.Set("Origin", "https://another.example")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
}
