//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type benchmarkSiteRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *benchmarkSiteRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return v, nil
}
func (r *benchmarkSiteRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *benchmarkSiteRepo) GetAll(context.Context) (map[string]string, error) { return r.values, nil }
func (r *benchmarkSiteRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func TestModelBenchmarkFederationAuthenticatesSiteAndRestrictsWorkerPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var paths []string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		require.Equal(t, "Bearer internal-secret", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("X-Site-ID"))
		require.Empty(t, r.Header.Get("X-User-ID"))
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	repo := &benchmarkSiteRepo{values: map[string]string{}}
	federation := service.NewModelBenchmarkFederationService(repo, nil)
	a, tokenA, err := federation.CreateSite(context.Background(), "甲站")
	require.NoError(t, err)
	b, tokenB, err := federation.CreateSite(context.Background(), "乙站")
	require.NoError(t, err)
	h := NewModelBenchmarkHandler()
	h.federation = federation
	router := gin.New()
	router.Any("/api/v1/model-benchmarks/federation/*path", h.Federation)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/model-benchmarks/federation"+path, strings.NewReader(`{"owner_user_id":9,"owner_site_id":"`+b.ID+`"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Site-ID", b.ID)
		req.Header.Set("X-User-ID", "9")
		req.Header.Set("Cookie", "session=browser-secret")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for _, token := range []string{"", "internal-secret", a.ID + tokenB[32:]} {
		require.Equal(t, 401, request("GET", "/state", token).Code)
	}
	require.Empty(t, paths)
	for _, path := range []string{"/users/7/state", "/users/7/runs?page=2", "/users/7/results?cursor=next", "/results", "/showcase", "/user-runs?user_id=7"} {
		response := request("GET", path, tokenA)
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Equal(t, "/sites/"+a.ID+path, paths[len(paths)-1])
	}
	response := request("POST", "/users/7/runs", tokenA)
	require.Equal(t, 200, response.Code)
	require.Equal(t, "/sites/"+a.ID+"/users/7/runs", paths[len(paths)-1])
	before := len(paths)
	for _, endpoint := range []struct{ method, path string }{{"PUT", "/execution"}, {"POST", "/runs"}, {"POST", "/bank/update"}, {"GET", "/sites/" + b.ID + "/state"}, {"GET", "/users/7/config"}, {"POST", "/runs/" + strings.Repeat("a", 32) + "/cancel"}, {"GET", "/users/7/runs/" + strings.Repeat("a", 32) + "/cancel"}} {
		require.Equal(t, 403, request(endpoint.method, endpoint.path, tokenA).Code, endpoint.path)
	}
	require.Len(t, paths, before)
	info := request("GET", "/info", tokenA)
	require.Equal(t, 200, info.Code)
	require.Contains(t, info.Body.String(), a.ID)
	require.NotContains(t, info.Body.String(), tokenA)
	require.NoError(t, federation.RevokeSite(context.Background(), a.ID))
	require.Equal(t, 401, request("GET", "/state", tokenA).Code)
	require.Equal(t, 200, request("GET", "/state", tokenB).Code)
	repo.values["model_benchmark_source"] = `{"source_url":"https://another.example/federation"}`
	require.Equal(t, 409, request("GET", "/info", tokenB).Code)
}

func TestModelBenchmarkRemoteModeNeverFallsBackToLocalWorker(t *testing.T) {
	var localHits int
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { localHits++; _, _ = w.Write([]byte(`{}`)) }))
	defer local.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", local.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	repo := &benchmarkSiteRepo{values: map[string]string{"model_benchmark_source": `{"source_url":"https://main.example/federation","encrypted_token":"invalid"}`}}
	h := NewModelBenchmarkHandler()
	h.federation = service.NewModelBenchmarkFederationService(repo, nil)
	router := gin.New()
	router.POST("/runs", h.Create)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("POST", "/runs", strings.NewReader(`{}`)))
	require.Equal(t, 403, response.Code)
	require.Zero(t, localHits)
	// A malformed persisted source fails closed before any local request.
	repo.values["model_benchmark_source"] = `{`
	router.GET("/state", h.State)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/state", nil))
	require.Equal(t, 503, response.Code)
	require.Zero(t, localHits)
}

func TestModelBenchmarkForeignUserIDsDoNotResolveToLocalAccounts(t *testing.T) {
	site := strings.Repeat("a", 32)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "foreign", "owner_user_id": 7, "owner_site_id": site}, {"id": "local", "owner_user_id": 7, "owner_site_id": ""}}, "page": 1, "page_size": 20})
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	h := NewModelBenchmarkHandler()
	users := &benchmarkUsersStub{}
	h.users = users
	router := gin.New()
	router.GET("/user-runs", h.UserRuns)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/user-runs", nil))
	require.Equal(t, 200, response.Code)
	require.Equal(t, 1, users.gets)
	var envelope struct {
		Data struct {
			Items []struct {
				Owner benchmarkOwner `json:"owner"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Equal(t, site, envelope.Data.Items[0].Owner.SiteID)
	require.Empty(t, envelope.Data.Items[0].Owner.Email)
	require.Equal(t, "alice@example.com", envelope.Data.Items[1].Owner.Email)
}

type benchmarkChangingSourceRepo struct {
	benchmarkSiteRepo
	sourceReads int
}

func (r *benchmarkChangingSourceRepo) GetValue(ctx context.Context, key string) (string, error) {
	if key == "model_benchmark_source" {
		r.sourceReads++
		if r.sourceReads == 1 {
			return "", nil
		}
		return `{"source_url":"https://new-main.example/federation","site_id":"new-site"}`, nil
	}
	return r.benchmarkSiteRepo.GetValue(ctx, key)
}

func TestModelBenchmarkUserRecordsUseOneConnectionSnapshot(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":"run","owner_user_id":7,"owner_site_id":""}],"page":1,"page_size":20}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	repo := &benchmarkChangingSourceRepo{}
	h := NewModelBenchmarkHandler()
	h.federation = service.NewModelBenchmarkFederationService(repo, nil)
	h.users = &benchmarkUsersStub{}
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/user-runs", h.UserRuns)
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest("GET", "/user-runs?timezone=Asia%2FShanghai", nil))
	require.Equal(t, 200, result.Code, result.Body.String())
	require.Equal(t, 1, repo.sourceReads)
	require.Contains(t, result.Body.String(), "alice@example.com")
}
