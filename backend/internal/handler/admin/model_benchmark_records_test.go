//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type benchmarkUsersStub struct {
	filters service.UserListFilters
	gets    int
}

func (s *benchmarkUsersStub) ListUsers(_ context.Context, page, pageSize int, filters service.UserListFilters, sortBy, sortOrder string) ([]service.User, int64, error) {
	s.filters = filters
	return []service.User{{ID: 7, Email: "alice@example.com", Username: "alice", PasswordHash: "never-return"}}, 31, nil
}

func (s *benchmarkUsersStub) GetUserIncludeDeleted(_ context.Context, id int64) (*service.User, error) {
	s.gets++
	if id != 7 {
		return nil, service.ErrUserNotFound
	}
	deleted := time.Now()
	return &service.User{ID: 7, Email: "alice@example.com", Username: "alice", PasswordHash: "never-return", DeletedAt: &deleted}, nil
}

func TestModelBenchmarkUserRecordsFiltersOwnersAndLookupDeduplication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/user-runs", r.URL.Path)
		require.Equal(t, "2", r.URL.Query().Get("page"))
		require.Equal(t, "7", r.URL.Query().Get("user_id"))
		require.Equal(t, "sol&x=1", r.URL.Query().Get("model"))
		require.Empty(t, r.URL.Query().Get("x"))
		require.False(t, r.URL.Query().Has("timezone"), "浏览器时区不能作为评测执行器的筛选条件")
		_, _ = w.Write([]byte(`{"items":[{"id":"one","owner_user_id":7},{"id":"two","owner_user_id":7},{"id":"gone","owner_user_id":8}],"total":23,"page":2,"page_size":20}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	h := NewModelBenchmarkHandler()
	users := &benchmarkUsersStub{}
	h.users = users
	router := gin.New()
	router.GET("/user-runs", h.UserRuns)
	w := httptest.NewRecorder()
	// 前端统一为每个 GET 请求附加时区参数。
	router.ServeHTTP(w, httptest.NewRequest("GET", "/user-runs?page=2&user_id=7&model=sol%26x%3D1&timezone=Asia%2FShanghai", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 2, users.gets)
	require.Contains(t, w.Body.String(), "alice@example.com")
	require.Contains(t, w.Body.String(), `"deleted":true`)
	require.Contains(t, w.Body.String(), `"missing":true`)
	require.NotContains(t, w.Body.String(), "never-return")
	require.NotContains(t, w.Body.String(), "internal-secret")
	for _, query := range []string{"page=0", "user_id=-1", "user_id=7&user_id=8", "owner_user_id=8", "model=x&callback=bad", "timezone=UTC&timezone=Asia%2FShanghai"} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/user-runs?"+query, nil))
		require.Equal(t, 400, w.Code)
	}
}

func TestModelBenchmarkUserOptionsOnlyReturnsAccountIdentity(t *testing.T) {
	users := &benchmarkUsersStub{}
	h := NewModelBenchmarkHandler()
	h.users = users
	router := gin.New()
	router.GET("/user-options", h.UserOptions)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/user-options?q=alice", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "alice", users.filters.AccountSearch)
	require.Empty(t, users.filters.Search)
	require.True(t, users.filters.IncludeDeleted)
	require.False(t, *users.filters.IncludeSubscriptions)
	require.NotContains(t, w.Body.String(), "never-return")
	var envelope struct {
		Data struct {
			Items   []benchmarkOwner `json:"items"`
			HasMore bool             `json:"has_more"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.True(t, envelope.Data.HasMore)
	require.Equal(t, "alice", envelope.Data.Items[0].Username)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/user-options?q="+strings.Repeat("x", 201), nil))
	require.Equal(t, 400, w.Code)
}

func TestModelBenchmarkSiteFiltersAndOptionsIncludeHistoricalSites(t *testing.T) {
	repo := &benchmarkSiteRepo{values: map[string]string{}}
	federation := service.NewModelBenchmarkFederationService(repo, nil)
	site, _, err := federation.CreateSite(context.Background(), "当前接入站")
	require.NoError(t, err)
	historicalID := strings.Repeat("a", 32)
	wantedSite := ""
	hits := 0
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		require.Equal(t, wantedSite, r.URL.Query().Get("site_id"))
		require.Equal(t, "2", r.URL.Query().Get("page"))
		require.Equal(t, "sol", r.URL.Query().Get("model"))
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "total": 0, "page": 1, "page_size": 20,
			"site_options": []benchmarkSiteOption{{ID: historicalID, Name: "历史外站"}}})
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	h := NewModelBenchmarkHandler()
	h.federation = federation
	router := gin.New()
	router.GET("/user-runs", h.UserRuns)
	for _, filter := range []string{"", "local", historicalID} {
		wantedSite = filter
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/user-runs?page=2&model=sol&site_id="+filter, nil))
		require.Equal(t, 200, w.Code, w.Body.String())
		var result struct {
			Data struct {
				Sites []benchmarkSiteOption `json:"site_options"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.ElementsMatch(t, []benchmarkSiteOption{{ID: "", Name: ""}, {ID: site.ID, Name: site.Name}, {ID: historicalID, Name: "历史外站"}}, result.Data.Sites)
	}
	for _, filter := range []string{"invalid", "local&site_id=" + historicalID, "'%20OR%201=1"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/user-runs?site_id="+filter, nil))
		require.Equal(t, 400, w.Code)
	}
	require.Equal(t, 3, hits)
	// A connected site's administrator cannot turn a filter into cross-site access.
	repo.values["model_benchmark_source"] = `{"source_url":"https://main.example/federation","site_id":"` + site.ID + `"}`
	router.GET("/user-options", h.UserOptions)
	for _, path := range []string{"/user-runs?site_id=local", "/user-runs?site_id=" + historicalID, "/user-options?q=remote&site_id=" + historicalID} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 400, w.Code)
	}
	require.Equal(t, 3, hits)
}

func TestModelBenchmarkAccountSearchFiltersSitesBeforeCombiningOptions(t *testing.T) {
	siteID := strings.Repeat("a", 32)
	hits := 0
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		require.Equal(t, siteID, r.URL.Query().Get("site_id"))
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []benchmarkOwner{{ID: 7, SiteID: siteID, Email: "remote@example.com"}}, "has_more": false})
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	h := NewModelBenchmarkHandler()
	h.users = &benchmarkUsersStub{}
	h.federation = service.NewModelBenchmarkFederationService(&benchmarkSiteRepo{values: map[string]string{}}, nil)
	router := gin.New()
	router.GET("/user-options", h.UserOptions)
	for _, tc := range []struct{ site, account, absent string }{{"local", "alice@example.com", "remote@example.com"}, {siteID, "remote@example.com", "alice@example.com"}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/user-options?q=user&site_id="+tc.site, nil))
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), tc.account)
		require.NotContains(t, w.Body.String(), tc.absent)
	}
	require.Equal(t, 1, hits, "local-only search must not query remote account snapshots")
}
