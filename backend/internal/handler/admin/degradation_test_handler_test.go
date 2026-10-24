package admin

import (
	"context"
	"encoding/json"
	"errors"
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
	plan      *service.DegradationTestPlan
	results   *degradationHandlerResultRepo
	statuses  []*service.DegradationTestStatus
	statusErr error
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

func (r *degradationHandlerPlanRepo) ListStatus(context.Context) ([]*service.DegradationTestStatus, error) {
	if r.statuses == nil {
		return []*service.DegradationTestStatus{}, r.statusErr
	}
	return r.statuses, r.statusErr
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
	router.GET("/plans/status", handler.ListStatus)
	return router, repo
}

func TestDegradationTestStatusReturnsPlanAndActualLatestResult(t *testing.T) {
	router, repo := newDegradationHandlerTest()
	repo.statuses = []*service.DegradationTestStatus{
		{Plan: &service.DegradationTestPlan{ID: 1, Enabled: false, ModelID: "current-model", ReasoningEffort: "xhigh"}, AccountName: "xzp-0.12", AccountPlatform: "openai",
			LatestResult: &service.DegradationTestResult{ID: 7, PlanID: 1, Status: "failed", ModelID: "original-model", ReasoningEffort: "high", ErrorMessage: "timeout"}},
		{Plan: &service.DegradationTestPlan{ID: 2, Enabled: true}, AccountName: "xzp-0.12", AccountPlatform: "openai"},
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plans/status", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var rows []service.DegradationTestStatus
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rows))
	require.Len(t, rows, 2)
	require.False(t, rows[0].Plan.Enabled)
	require.Equal(t, "xzp-0.12", rows[0].AccountName)
	require.Equal(t, "openai", rows[0].AccountPlatform)
	require.Equal(t, "failed", rows[0].LatestResult.Status)
	require.Equal(t, "original-model", rows[0].LatestResult.ModelID)
	require.Equal(t, "high", rows[0].LatestResult.ReasoningEffort)
	require.Nil(t, rows[1].LatestResult)
	require.Contains(t, response.Body.String(), `"latest_result":null`)
}

func TestDegradationTestStatusEmptyAndFailure(t *testing.T) {
	router, repo := newDegradationHandlerTest()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plans/status", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `[]`, response.Body.String())
	repo.statusErr = errors.New("database unavailable")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plans/status", nil))
	require.Equal(t, http.StatusInternalServerError, response.Code)
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
