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

func TestModelBenchmarkUserAccountSnapshotCannotBeChosenByBrowser(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Identity benchmarkIdentity `json:"_benchmark_identity"`
			Model    string            `json:"model"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "alice@example.com", body.Identity.Email)
		require.Equal(t, "alice", body.Identity.Username)
		require.Empty(t, body.Identity.SiteName)
		require.Equal(t, "test-model", body.Model)
		require.True(t, strings.HasPrefix(r.URL.Path, "/users/7/runs"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	h := NewModelBenchmarkHandler()
	h.users = &benchmarkUsersStub{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	router.POST("/api/v1/model-benchmarks/runs", h.User)
	router.POST("/api/v1/model-benchmarks/runs/:id/rerun", h.User)
	for _, path := range []string{"/runs", "/runs/" + strings.Repeat("a", 32) + "/rerun"} {
		req := httptest.NewRequest("POST", "/api/v1/model-benchmarks"+path, strings.NewReader(`{"model":"test-model","owner_user_id":8,"_benchmark_identity":{"email":"forged@example.com","username":"forged","site_name":"forged"}}`))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
}

func TestModelBenchmarkFederatedSnapshotUsesVerifiedSiteLabel(t *testing.T) {
	repo := &benchmarkSiteRepo{values: map[string]string{}}
	federation := service.NewModelBenchmarkFederationService(repo, nil)
	site, token, err := federation.CreateSite(context.Background(), "甲站 api.example.com")
	require.NoError(t, err)
	hits := 0
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		require.Equal(t, "/sites/"+site.ID+"/users/7/runs", r.URL.Path)
		var body struct {
			Identity benchmarkIdentity `json:"_benchmark_identity"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, site.Name, body.Identity.SiteName)
		if hits == 1 {
			require.Equal(t, "remote@example.com", body.Identity.Email)
		} else {
			require.Empty(t, body.Identity.Email)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	h := NewModelBenchmarkHandler()
	h.federation = federation
	router := gin.New()
	router.POST("/federation/*path", h.Federation)
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/federation/users/7/runs", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, 200, request(`{"_benchmark_identity":{"email":"remote@example.com","username":"remote","site_name":"spoofed","password":"secret"}}`).Code)
	require.Equal(t, 200, request(`{}`).Code, "legacy sites can still submit")
	for _, identity := range []string{`[]`, `{"email":3}`, `{"email":"bad\nvalue"}`, `{"username":"` + strings.Repeat("x", 201) + `"}`} {
		require.Equal(t, 400, request(`{"_benchmark_identity":`+identity+`}`).Code)
	}
	require.Equal(t, 2, hits)
}

func TestModelBenchmarkUserRecordsResolveSavedRemoteAccountsAndSiteNames(t *testing.T) {
	repo := &benchmarkSiteRepo{values: map[string]string{}}
	federation := service.NewModelBenchmarkFederationService(repo, nil)
	site, _, err := federation.CreateSite(context.Background(), "甲站")
	require.NoError(t, err)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, site.ID, r.URL.Query().Get("site_id"))
		require.Equal(t, "7", r.URL.Query().Get("user_id"))
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"id": "new", "owner_user_id": 7, "owner_site_id": site.ID, "owner_email": "remote@example.com", "owner_username": "remote", "owner_site_name": "甲站", "base_url": "https://test.example.com/v1"},
			{"id": "old", "owner_user_id": 8, "owner_site_id": site.ID},
		}, "page": 1, "page_size": 20, "total": 2})
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	h := NewModelBenchmarkHandler()
	h.federation = federation
	users := &benchmarkUsersStub{}
	h.users = users
	router := gin.New()
	router.GET("/user-runs", h.UserRuns)
	for _, revoked := range []bool{false, true} {
		if revoked {
			require.NoError(t, federation.RevokeSite(context.Background(), site.ID))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/user-runs?user_id=7&site_id="+site.ID, nil))
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Zero(t, users.gets)
		var result struct {
			Data struct {
				Items []struct {
					Owner   benchmarkOwner `json:"owner"`
					BaseURL string         `json:"base_url"`
				} `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Equal(t, "remote@example.com", result.Data.Items[0].Owner.Email)
		require.Equal(t, "remote", result.Data.Items[0].Owner.Username)
		require.Equal(t, "甲站", result.Data.Items[0].Owner.SiteName)
		require.Equal(t, "https://test.example.com/v1", result.Data.Items[0].BaseURL)
		require.True(t, result.Data.Items[1].Owner.Missing)
		if !revoked {
			require.Equal(t, "甲站", result.Data.Items[1].Owner.SiteName)
		}
	}
}

func TestModelBenchmarkAccountOptionsCombineLocalAndRemoteWithoutIDCollision(t *testing.T) {
	siteID := strings.Repeat("a", 32)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/user-options", r.URL.Path)
		require.Equal(t, "alice&admin", r.URL.Query().Get("q"))
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"id": 7, "site_id": siteID, "site_name": "甲站", "email": "remote@example.com", "username": "remote", "api_key": "never-return"},
		}, "has_more": false})
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "internal-secret")
	h := NewModelBenchmarkHandler()
	h.users = &benchmarkUsersStub{}
	h.federation = service.NewModelBenchmarkFederationService(&benchmarkSiteRepo{values: map[string]string{}}, nil)
	router := gin.New()
	router.GET("/user-options", h.UserOptions)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/user-options?q=alice%26admin", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		Data struct {
			Items []benchmarkOwner `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result.Data.Items, 2)
	require.Equal(t, result.Data.Items[0].ID, result.Data.Items[1].ID)
	require.Empty(t, result.Data.Items[0].SiteID)
	require.Equal(t, siteID, result.Data.Items[1].SiteID)
	require.NotContains(t, w.Body.String(), "never-return")
}
