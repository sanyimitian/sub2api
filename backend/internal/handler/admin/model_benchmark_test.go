//go:build unit

package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestModelBenchmarkProxyCredentialsAndEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runs" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer worker-secret" || r.Header.Get("Cookie") != "" {
			t.Error("browser credentials forwarded to worker")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"effort":"medium"`) {
			t.Error("request parameters were not preserved")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"abc","status":"queued","pass_rate":null}`))
	}))
	defer worker.Close()
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-secret")
	h := NewModelBenchmarkHandler()
	router := gin.New()
	router.POST("/runs", h.Create)
	req := httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(`{"effort":"medium","api_key":"model-key"}`))
	req.Header.Set("Authorization", "Bearer browser-session")
	req.Header.Set("Cookie", "session=browser-cookie")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var envelope struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Code != 0 || envelope.Data["status"] != "queued" {
		t.Fatalf("invalid envelope: %s", w.Body)
	}
}

func TestModelBenchmarkProxyFailuresAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"validation", 409, `{"message":"Suite changed","private":"not-public"}`, 409},
		{"invalid json", 200, `upstream-secret-not-json`, 502},
		{"redirect", 302, `{}`, 502},
		{"worker auth failure", 401, `{"message":"Unauthorized"}`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://127.0.0.1:1/")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer worker.Close()
			t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
			t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-secret")
			router := gin.New()
			router.GET("/state", NewModelBenchmarkHandler().State)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/state", nil))
			if w.Code != tc.want || strings.Contains(w.Body.String(), "not-public") || strings.Contains(w.Body.String(), "upstream-secret") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", "http://127.0.0.1:1")
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-secret")
	router := gin.New()
	h := NewModelBenchmarkHandler()
	router.POST("/runs", h.Create)
	router.GET("/runs/:id", h.Detail)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(strings.Repeat("a", 16385))),
		httptest.NewRequest(http.MethodGet, "/runs/not-an-id", nil),
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected input rejection, got %d", w.Code)
		}
	}
}

func TestModelBenchmarkUnconfigured(t *testing.T) {
	t.Setenv("MODEL_BENCHMARK_WORKER_URL", "")
	t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "")
	router := gin.New()
	router.GET("/state", NewModelBenchmarkHandler().State)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/state", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
