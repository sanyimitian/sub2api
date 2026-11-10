package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelIdentificationRoutesSeparateSharedReadsFromAdminWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &handler.Handlers{ModelIdentification: handler.NewModelIdentificationHandler(nil, nil, nil)}
	public := router.Group("/api/v1", func(c *gin.Context) { c.AbortWithStatus(http.StatusNoContent) })
	admin := router.Group("/api/v1/admin", func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) })
	registerModelIdentificationReadRoutes(public, h)
	registerModelIdentificationRoutes(admin, h)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/admin/model-identification/plans/1", 403},
		{"GET", "/api/v1/model-identification/plans/1", 404},
		{"GET", "/api/v1/model-identification/groups", 204},
		{"GET", "/api/v1/model-identification/plans/1/results", 204},
		{"POST", "/api/v1/model-identification/plans", 404},
		{"POST", "/api/v1/model-identification/plans/1/run", 404},
		{"PUT", "/api/v1/model-identification/plans/1", 404},
		{"DELETE", "/api/v1/model-identification/plans/1", 404},
		{"POST", "/api/v1/admin/model-identification/plans", 403},
		{"POST", "/api/v1/admin/model-identification/plans/1/run", 403},
		{"PUT", "/api/v1/admin/model-identification/plans/1", 403},
		{"DELETE", "/api/v1/admin/model-identification/plans/1", 403},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, tc.status, w.Code, tc.path)
	}
}
