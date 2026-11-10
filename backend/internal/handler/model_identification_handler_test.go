package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type identificationHandlerStub struct {
	modelIdentificationService
	admin bool
	err   error
	saved service.ModelIdentificationInput
}

func (s *identificationHandlerStub) Results(_ context.Context, _ int64, admin bool) ([]*service.DegradationTestResult, error) {
	s.admin = admin
	return []*service.DegradationTestResult{}, s.err
}
func (s *identificationHandlerStub) GetPlan(context.Context, int64) (*service.DegradationTestPlan, error) {
	id := int64(4)
	return &service.DegradationTestPlan{ID: 1, GroupID: &id}, s.err
}
func (s *identificationHandlerStub) Save(_ context.Context, _ int64, input service.ModelIdentificationInput) (*service.DegradationTestPlan, error) {
	s.saved = input
	return &service.DegradationTestPlan{ID: 1}, s.err
}

type identificationRunnerStub struct{ err error }

func (s *identificationRunnerStub) RunNow(context.Context, int64) (*service.DegradationTestPlan, error) {
	return &service.DegradationTestPlan{ID: 1}, s.err
}

func TestModelIdentificationHandlerUsesAuthenticatedRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"user", "admin"} {
		stub := &identificationHandlerStub{}
		h := &ModelIdentificationHandler{svc: stub}
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUserRole), role) })
		router.GET("/plans/:id/results", h.Results)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/plans/1/results?admin=true", nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, role == "admin", stub.admin)
		stub.err = sql.ErrNoRows
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/plans/1/results", nil))
		require.Equal(t, http.StatusNotFound, w.Code)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/plans/0/results", nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
}
func TestModelIdentificationRunReturnsAcceptedAndHonestConflicts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &identificationHandlerStub{}
	runner := &identificationRunnerStub{}
	h := &ModelIdentificationHandler{svc: stub, runner: runner}
	router := gin.New()
	router.POST("/plans/:id/run", h.Run)
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 202}, {service.ErrDegradationTestRunning, 409}, {service.ErrDegradationTestCapacity, 503}, {service.ErrDegradationTestStopped, 503}} {
		runner.err = tc.err
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/plans/1/run", nil))
		require.Equal(t, tc.status, w.Code)
	}
}
func TestModelIdentificationDuplicateGroupReturnsConflictWithoutDatabaseDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &identificationHandlerStub{err: &pq.Error{Code: "23505", Message: "private database detail"}}
	h := &ModelIdentificationHandler{svc: stub}
	router := gin.New()
	router.POST("/plans", h.Create)
	w := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/plans", strings.NewReader("{\"display_group_id\":42,\"model_id\":\"real-model\",\"interval_minutes\":30,\"enabled\":true}"))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, request)
	require.Equal(t, http.StatusConflict, w.Code)
	require.NotContains(t, w.Body.String(), "private database")
	require.EqualValues(t, 42, stub.saved.DisplayGroupID)
}

func (s *identificationHandlerStub) AdminConfig(context.Context, int64) (*service.ModelIdentificationAdminConfig, error) {
	return &service.ModelIdentificationAdminConfig{Name: "Group", Endpoint: "https://private.example.com", APIKey: "admin-secret", Protocol: "responses"}, s.err
}
func (s *identificationHandlerStub) ListGroups(context.Context) ([]*service.ModelIdentificationGroup, error) {
	return []*service.ModelIdentificationGroup{{GroupID: 4, Name: "Group", Platform: "responses", Plan: &service.DegradationTestPlan{ID: 1}}}, s.err
}
func TestModelIdentificationAdminConfigHasNoStoreAndPublicListHasNoSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ModelIdentificationHandler{svc: &identificationHandlerStub{}}
	router := gin.New()
	router.GET("/config/:id", h.Config)
	router.GET("/groups", h.List)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/config/1", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Body.String(), "admin-secret")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/groups", nil))
	require.Equal(t, 200, w.Code)
	for _, secret := range []string{"admin-secret", "private.example.com", "endpoint", "api_key"} {
		require.NotContains(t, w.Body.String(), secret)
	}
}
