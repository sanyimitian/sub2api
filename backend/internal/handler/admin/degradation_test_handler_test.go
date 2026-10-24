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

type degradationHandlerPlanRepo struct {
	service.DegradationTestPlanRepository
	plan    *service.DegradationTestPlan
	results *degradationHandlerResultRepo
}

type degradationHandlerResultRepo struct {
	service.DegradationTestResultRepository
	limit int
}

func (r *degradationHandlerResultRepo) ListByPlanID(_ context.Context, _ int64, limit int) ([]*service.DegradationTestResult, error) {
	r.limit = limit
	return []*service.DegradationTestResult{}, nil
}

func (r *degradationHandlerPlanRepo) Create(_ context.Context, plan *service.DegradationTestPlan) (*service.DegradationTestPlan, error) {
	plan.ID = 1
	r.plan = plan
	return plan, nil
}

func (r *degradationHandlerPlanRepo) GetByID(context.Context, int64) (*service.DegradationTestPlan, error) {
	plan := *r.plan
	return &plan, nil
}

func (r *degradationHandlerPlanRepo) Update(_ context.Context, plan *service.DegradationTestPlan) (*service.DegradationTestPlan, error) {
	r.plan = plan
	return plan, nil
}

func newDegradationHandlerTest() (*gin.Engine, *degradationHandlerPlanRepo) {
	gin.SetMode(gin.TestMode)
	repo := &degradationHandlerPlanRepo{results: &degradationHandlerResultRepo{}}
	handler := NewDegradationTestHandler(service.NewDegradationTestService(repo, repo.results), nil)
	router := gin.New()
	router.POST("/plans", handler.Create)
	router.PUT("/plans/:id", handler.Update)
	router.GET("/plans/:id/results", handler.ListResults)
	return router, repo
}

func TestDegradationTestCreateDefaultAndCustomPrompt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		wantPrompt string
	}{
		{"default", `{"account_id":1,"model_id":"selected","cron_expression":"@every 90m"}`, service.DefaultDegradationTestPrompt},
		{"custom", `{"account_id":1,"model_id":"selected","cron_expression":"0 * * * *","prompt":"write SVG"}`, "write SVG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, repo := newDegradationHandlerTest()
			request := httptest.NewRequest(http.MethodPost, "/plans", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, tc.wantPrompt, repo.plan.Prompt)
			require.Equal(t, 24, repo.plan.MaxResults)
			require.NotNil(t, repo.plan.NextRunAt)
		})
	}
}

func TestDegradationTestUpdateRejectsClearingPromptAndModel(t *testing.T) {
	router, repo := newDegradationHandlerTest()
	repo.plan = &service.DegradationTestPlan{ID: 1, AccountID: 1, ModelID: "old-model", Prompt: "old prompt", CronExpression: "@every 30m", MaxResults: 50}
	request := httptest.NewRequest(http.MethodPut, "/plans/1", strings.NewReader(`{"prompt":"","model_id":""}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Equal(t, "old prompt", repo.plan.Prompt)
	require.Equal(t, "old-model", repo.plan.ModelID)
	require.Equal(t, "@every 30m", repo.plan.CronExpression)
	require.Equal(t, 50, repo.plan.MaxResults)
}

func TestDegradationTestRejectsInvalidCreateInputs(t *testing.T) {
	for _, changes := range []map[string]any{
		{"account_id": -1},
		{"prompt": ""}, {"prompt": "  "}, {"model_id": ""},
		{"prompt": strings.Repeat("a", 20001)}, {"cron_expression": "@every 5s"},
		{"reasoning_effort": "extreme"},
	} {
		router, repo := newDegradationHandlerTest()
		body := map[string]any{"account_id": 1, "model_id": "selected", "cron_expression": "@every 30m"}
		for key, value := range changes {
			body[key] = value
		}
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/plans", strings.NewReader(string(payload)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		require.Nil(t, repo.plan)
	}
}

func TestDegradationTestReasoningEffortCreateUpdateAndClear(t *testing.T) {
	router, repo := newDegradationHandlerTest()
	for _, tc := range []struct{ method, path, body, want string }{
		{http.MethodPost, "/plans", `{"account_id":1,"model_id":"gpt-5","cron_expression":"@every 30m","reasoning_effort":"xhigh"}`, "xhigh"},
		{http.MethodPut, "/plans/1", `{"prompt":"new prompt"}`, "xhigh"},
		{http.MethodPut, "/plans/1", `{"reasoning_effort":"high"}`, "high"},
		{http.MethodPut, "/plans/1", `{"reasoning_effort":""}`, ""},
	} {
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, tc.want, repo.plan.ReasoningEffort)
		var returned service.DegradationTestPlan
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &returned))
		require.Equal(t, tc.want, returned.ReasoningEffort)
	}
}

func TestDegradationTestRejectsInvalidResultLimit(t *testing.T) {
	for _, limit := range []string{"0", "-1", "bad"} {
		router, _ := newDegradationHandlerTest()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plans/1/results?limit="+limit, nil))
		require.Equal(t, http.StatusBadRequest, response.Code)
	}
}

func TestDegradationTestIgnoresLegacyRetentionOnCreateAndUpdate(t *testing.T) {
	for _, requested := range []int{-1, 0, 1, 10, 24, 50, 1001} {
		router, repo := newDegradationHandlerTest()
		payload, err := json.Marshal(map[string]any{
			"account_id": 1, "model_id": "selected", "cron_expression": "@every 30m", "max_results": requested,
		})
		require.NoError(t, err)
		for _, operation := range []struct{ method, path string }{{http.MethodPost, "/plans"}, {http.MethodPut, "/plans/1"}} {
			request := httptest.NewRequest(operation.method, operation.path, strings.NewReader(string(payload)))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, 24, repo.plan.MaxResults)
			var returned service.DegradationTestPlan
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &returned))
			require.Equal(t, 24, returned.MaxResults)
		}
	}
}

func TestDegradationTestListResultsClampsLegacyLimit(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 10}, {"?limit=24", 24}, {"?limit=50", 24}, {"?limit=1001", 24}, {"?limit=3", 3}, {"?limit=11", 11}} {
		router, repo := newDegradationHandlerTest()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plans/1/results"+tc.query, nil))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, tc.want, repo.results.limit)
	}
}
