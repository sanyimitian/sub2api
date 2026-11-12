//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type benchmarkTargetStub struct{ calls int }

func (*benchmarkTargetStub) Groups(context.Context, int64) ([]service.BenchmarkGroupOption, error) {
	return []service.BenchmarkGroupOption{{ID: 4, Name: "Premium"}}, nil
}
func (*benchmarkTargetStub) Models(context.Context, int64, int64) ([]string, error) {
	return []string{"model-a"}, nil
}
func (s *benchmarkTargetStub) Target(_ context.Context, userID, groupID int64, model, effort string) (*service.BenchmarkGroupTarget, error) {
	s.calls++
	return &service.BenchmarkGroupTarget{GroupID: groupID, GroupName: "Trusted group", BaseURL: "https://local.example/v1", APIKey: "server-only-secret", Model: model, Effort: effort}, nil
}
func TestModelBenchmarkGroupScheduleResolvesTargetOnServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var received map[string]any
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/schedules", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"schedule","group_name":"Trusted group"}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-secret")
	h := NewModelBenchmarkHandler()
	svc := &benchmarkTargetStub{}
	h.groups = svc
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
	})
	router.POST("/schedules", h.Schedules)
	base := `{"group_id":4,"model":"model-a","name":"Daily","cron_expression":"@every 1h","enabled":true,"public_results":true}`
	for _, field := range []string{`"base_url":"https://attacker.example"`, `"api_key":"browser-key"`, `"group_name":"fake"`, `"owner_user_id":999`} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/schedules", strings.NewReader(strings.TrimSuffix(base, "}")+","+field+"}")))
		require.Equal(t, 400, w.Code)
	}
	require.Zero(t, svc.calls)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/schedules", strings.NewReader(base))
	req.Host = "attacker.example"
	router.ServeHTTP(w, req)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "https://local.example/v1", received["base_url"])
	require.Equal(t, "server-only-secret", received["api_key"])
	require.Equal(t, "medium", received["effort"])
	require.Equal(t, "Trusted group", received["group_name"])
	require.NotContains(t, w.Body.String(), "secret")
}

func TestModelBenchmarkGroupPublicFeedHidesURL(t *testing.T) {
	fixture := benchmarkPublicFixture()
	item := fixture["items"].([]any)[0].(map[string]any)
	item["group_id"], item["group_name"], item["base_url"] = 4, "Premium", "https://local.example/v1"
	body, err := json.Marshal(fixture)
	require.NoError(t, err)
	feed, err := decodeBenchmarkPublicFeed(body)
	require.NoError(t, err)
	require.Empty(t, feed.Items[0].BaseURL)
	require.Equal(t, "Premium", feed.Items[0].GroupName)
	for _, bad := range []any{-1, true, "4"} {
		item["group_id"] = bad
		body, _ = json.Marshal(fixture)
		_, err = decodeBenchmarkPublicFeed(body)
		require.Error(t, err)
	}
}
