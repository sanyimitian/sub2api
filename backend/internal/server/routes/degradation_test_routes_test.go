package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDegradationTestRoutesRemainIndependentFromScheduledTests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminRoutes := router.Group("/admin")
	h := &handler.Handlers{Admin: &handler.AdminHandlers{
		ScheduledTest:   admin.NewScheduledTestHandler(nil),
		DegradationTest: admin.NewDegradationTestHandler(nil, nil),
	}}
	registerScheduledTestRoutes(adminRoutes, h)
	registerDegradationTestRoutes(adminRoutes, h)
	registered := make(map[string]string)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = route.Handler
	}
	for _, feature := range []struct{ path, handlerName string }{
		{"scheduled-test-plans", "ScheduledTestHandler"},
		{"degradation-test-plans", "DegradationTestHandler"},
	} {
		for _, route := range []struct{ method, suffix string }{
			{http.MethodPost, ""}, {http.MethodPut, "/:id"}, {http.MethodDelete, "/:id"}, {http.MethodGet, "/:id/results"},
		} {
			name := route.method + " /admin/" + feature.path + route.suffix
			require.Contains(t, registered[name], feature.handlerName, name)
		}
		require.Contains(t, registered["GET /admin/accounts/:id/"+feature.path], feature.handlerName)
	}
	require.Contains(t, registered["POST /admin/degradation-test-plans/:id/run"], "DegradationTestHandler")
	require.NotContains(t, registered, "POST /admin/scheduled-test-plans/:id/run")
}
