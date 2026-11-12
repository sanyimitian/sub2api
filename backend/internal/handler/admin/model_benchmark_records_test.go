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
		require.False(t, r.URL.Query().Has("timezone"), "browser timezone must not become a worker filter")
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
	// The shared browser client adds timezone to every GET request.
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
