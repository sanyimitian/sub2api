//go:build unit

package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func benchmarkPublicFixture() map[string]any {
	return map[string]any{"version": 2, "generated_at": "2026-11-12T01:00:00Z", "limit": 20, "items": []any{
		map[string]any{"id": strings.Repeat("a", 32), "schedule_id": strings.Repeat("b", 32), "schedule_name": "Public plan", "model": "test-model", "effort": "medium", "status": "completed", "created_at": "2026-11-12T00:00:00Z", "finished_at": "2026-11-12T00:01:00Z", "total": 1, "completed": 1, "passed": 1, "pass_rate": 100, "categories": map[string]any{
			"coding": map[string]int{"total": 0, "completed": 0, "passed": 0}, "math": map[string]int{"total": 1, "completed": 1, "passed": 1}, "reasoning": map[string]int{"total": 0, "completed": 0, "passed": 0},
		}, "suite_day": "2026-11-12", "suite_revision": 1, "suite_fingerprint": strings.Repeat("c", 64), "elapsed_seconds": 60, "usage": map[string]int{"input_tokens": 10, "output_tokens": 20, "total_tokens": 30}, "api_errors": 0, "grading_errors": 0},
	}}
}

func benchmarkPublicTestRouter(t *testing.T, fn http.HandlerFunc) (*gin.Engine, *ModelBenchmarkHandler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	worker := httptest.NewServer(fn)
	t.Cleanup(worker.Close)
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-secret")
	h := NewModelBenchmarkHandler()
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Any("/public-results", h.PublicResults)
	return router, h
}

func TestModelBenchmarkPublicRejectsInjectedInputsAndMethods(t *testing.T) {
	var hits atomic.Int32
	router, _ := benchmarkPublicTestRouter(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
	for _, tc := range []struct {
		method, target, body string
		status               int
	}{
		{"GET", "/public-results?url=http://127.0.0.1/admin", "", 400},
		{"GET", "/public-results?limit=999999&callback=alert", "", 400},
		{"GET", "/public-results?sort=DROP%20TABLE%20runs", "", 400},
		{"GET", "/public-results", "{\"owner_user_id\":1}", 400},
		{"POST", "/public-results", "{}", 405},
		{"DELETE", "/public-results", "", 405},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body)))
		require.Equal(t, tc.status, w.Code)
	}
	require.Zero(t, hits.Load())
}

func TestModelBenchmarkPublicProjectsOnlyAllowedFields(t *testing.T) {
	fixture := benchmarkPublicFixture()
	fixture["credential"] = "root-secret"
	item := fixture["items"].([]any)[0].(map[string]any)
	for _, name := range []string{"credential", "answer", "error", "api_key"} {
		item[name] = "private-secret"
	}
	item["schedule_name"] = "<img src=x onerror=alert(1)>"
	item["pass_rate"] = 999
	item["base_url"] = "https://test.one/v1"
	router, _ := benchmarkPublicTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/public-results", r.URL.RequestURI())
		require.Equal(t, "Bearer worker-secret", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("X-Forwarded-Host"))
		require.EqualValues(t, 0, r.ContentLength)
		_ = json.NewEncoder(w).Encode(fixture)
	})
	req := httptest.NewRequest("GET", "/public-results", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	req.Header.Set("Cookie", "session=private")
	req.Header.Set("X-Forwarded-Host", "127.0.0.1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	require.NotContains(t, w.Body.String(), "secret")
	require.NotContains(t, w.Body.String(), "<img")
	var result struct {
		Data benchmarkPublicFeed `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Equal(t, float64(100), *result.Data.Items[0].PassRate)
	require.Equal(t, "https://test.one/v1", result.Data.Items[0].BaseURL)
}

func TestModelBenchmarkPublicRejectsMalformedAndOversizedWorkerData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"too many", func(f map[string]any) {
			item := f["items"].([]any)[0]
			items := make([]any, 21)
			for i := range items {
				items[i] = item
			}
			f["items"] = items
		}},
		{"invalid id", func(f map[string]any) { f["items"].([]any)[0].(map[string]any)["id"] = "';DROP TABLE runs;--" }},
		{"bad counts", func(f map[string]any) { f["items"].([]any)[0].(map[string]any)["passed"] = 2 }},
		{"negative incomplete count", func(f map[string]any) { f["items"].([]any)[0].(map[string]any)["incomplete_responses"] = -1 }},
		{"excess incomplete count", func(f map[string]any) { f["items"].([]any)[0].(map[string]any)["incomplete_responses"] = 2 }},
		{"unknown category", func(f map[string]any) {
			f["items"].([]any)[0].(map[string]any)["categories"].(map[string]any)["secret"] = map[string]int{}
		}},
		{"bad time", func(f map[string]any) { f["items"].([]any)[0].(map[string]any)["finished_at"] = "not-time" }},
		{"oversized", func(f map[string]any) { f["padding"] = strings.Repeat("a", benchmarkPublicMaxBytes) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := benchmarkPublicFixture()
			tc.change(f)
			router, _ := benchmarkPublicTestRouter(t, func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(f) })
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/public-results", nil))
			require.Equal(t, 502, w.Code)
		})
	}
}

func TestModelBenchmarkPublicPreservesIncompleteResponses(t *testing.T) {
	fixture := benchmarkPublicFixture()
	item := fixture["items"].([]any)[0].(map[string]any)
	item["status"] = "failed"
	item["passed"] = 0
	item["categories"].(map[string]any)["math"].(map[string]int)["passed"] = 0
	item["incomplete_responses"] = 1
	router, _ := benchmarkPublicTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fixture)
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/public-results", nil))
	require.Equal(t, 200, w.Code)
	var result struct {
		Data benchmarkPublicFeed `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Equal(t, 1, result.Data.Items[0].IncompleteResponses)
	require.Zero(t, result.Data.Items[0].APIErrors)
	require.Nil(t, result.Data.Items[0].PassRate)
}

func TestModelBenchmarkPublicHidesWorkerErrorsAndRedirects(t *testing.T) {
	for _, status := range []int{401, 500, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			router, _ := benchmarkPublicTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://127.0.0.1/private")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"private-secret"}`))
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/public-results", nil))
			require.Equal(t, 503, w.Code)
			require.NotContains(t, w.Body.String(), "private-secret")
			require.Empty(t, w.Header().Get("Location"))
		})
	}
}

func TestModelBenchmarkPublicRateLimitsCannotBeBypassedByForwardedHeaders(t *testing.T) {
	var hits atomic.Int32
	router, _ := benchmarkPublicTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(benchmarkPublicFixture())
	})
	for i := 0; i <= 10; i++ {
		req := httptest.NewRequest("GET", "/public-results", nil)
		req.RemoteAddr = "198.51.100.10:1234"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("8.8.8.%d", i+1))
		req.Header.Set("X-Real-IP", fmt.Sprintf("8.8.4.%d", i+1))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if i < 10 {
			require.Equal(t, 200, w.Code)
		} else {
			require.Equal(t, 429, w.Code)
			require.NotEmpty(t, w.Header().Get("Retry-After"))
		}
	}
	require.EqualValues(t, 10, hits.Load())
	guard := newBenchmarkPublicGuard()
	now := time.Now()
	for i := 0; i < 20; i++ {
		require.True(t, guard.allow(fmt.Sprint(i), now))
	}
	require.False(t, guard.allow("another", now))
	require.True(t, guard.allow("another", now.Add(time.Second)))
}

func TestModelBenchmarkPublicConcurrencyLimitReleasesAfterCompletion(t *testing.T) {
	entered, release := make(chan struct{}, 8), make(chan struct{})
	router, _ := benchmarkPublicTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		_ = json.NewEncoder(w).Encode(benchmarkPublicFixture())
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	done := make(chan int, 4)
	for i := 0; i < 4; i++ {
		go func() {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/public-results", nil))
			done <- w.Code
		}()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("worker request did not arrive")
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/public-results", nil))
	require.Equal(t, 429, w.Code)
	close(release)
	for i := 0; i < 4; i++ {
		require.Equal(t, 200, <-done)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/public-results", nil))
	require.Equal(t, 200, w.Code)
}
