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
			want := "no-store"
			if path == "/v2/snapshot" || (path == "/snapshot" && passive) {
				want = "public, max-age=60"
			}
			require.Equal(t, want, response.Header().Get("Cache-Control"), path)
		}
	}
}
