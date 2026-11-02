//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type transitMonitorPrivacyService struct {
	passive bool
	sections *service.PublicTransitExportSections
}

func (s transitMonitorPrivacyService) Discovery(context.Context, string) (*service.PublicTransitDiscovery, error) {
	return &service.PublicTransitDiscovery{}, nil
}
func (s transitMonitorPrivacyService) Snapshot(context.Context, string) (*service.PublicTransitSnapshot, error) {
	return &service.PublicTransitSnapshot{}, nil
}
func (s transitMonitorPrivacyService) SnapshotAuto(context.Context, string, string) (any, error) {
	if s.passive {
		return &service.PublicTransitSnapshotV2{}, nil
	}
	return &service.PublicTransitSnapshot{}, nil
}
func (s transitMonitorPrivacyService) SnapshotV2Range(context.Context, string, string) (*service.PublicTransitSnapshotV2, error) {
	return &service.PublicTransitSnapshotV2{}, nil
}
func (s transitMonitorPrivacyService) GetExportSections(context.Context) (service.PublicTransitExportSections, error) {
	if s.sections != nil {
		return *s.sections, nil
	}
	return service.DefaultPublicTransitExportSections(), nil
}
func (s transitMonitorPrivacyService) SetExportSections(context.Context, service.PublicTransitExportSections) error {
	return nil
}
func (s transitMonitorPrivacyService) PublicSnapshot(_ context.Context, snapshot any) (map[string]any, error) {
	sections, _ := s.GetExportSections(context.Background())
	return service.FilterPublicTransitSnapshot(snapshot, sections)
}

func TestPublicTransitExportSectionsApplyToAllSnapshotRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sections := service.DefaultPublicTransitExportSections()
	sections.Billing = false
	h := &PublicTransitHandler{publicTransitService: transitMonitorPrivacyService{sections: &sections}}
	router := gin.New()
	router.GET("/snapshot", h.SnapshotAuto)
	router.GET("/v1/snapshot", h.Snapshot)
	router.GET("/v2/snapshot", h.SnapshotV2)
	for _, path := range []string{"/snapshot", "/v1/snapshot", "/v2/snapshot"} {
		result := httptest.NewRecorder()
		router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, result.Code)
		require.NotContains(t, result.Body.String(), `"billing"`)
		require.Contains(t, result.Body.String(), `"schema_version"`)
	}
}

func TestPublicTransitManualMonitorSnapshotsCannotBeCachedAcrossVisibilityChanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passive := range []bool{false, true} {
		h := &PublicTransitHandler{publicTransitService: transitMonitorPrivacyService{passive: passive}}
		router := gin.New()
		router.GET("/snapshot", h.SnapshotAuto)
		router.GET("/v1/snapshot", h.Snapshot)
		router.GET("/v2/snapshot", h.SnapshotV2)
		for _, path := range []string{"/snapshot", "/v1/snapshot", "/v2/snapshot"} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"), path)
		}
	}
}
