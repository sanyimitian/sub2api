package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsQQWebhookHTTPBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewOpsHandler(&service.OpsService{})
	r.POST(service.OpsQQWebhookPath, h.ReceiveQQWebhook)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"op":13}`, http.StatusServiceUnavailable},
		{strings.Repeat("x", service.OpsQQWebhookBodyLimit+1), http.StatusRequestEntityTooLarge},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, service.OpsQQWebhookPath, strings.NewReader(tc.body)))
		require.Equal(t, tc.status, w.Code)
		require.Empty(t, w.Body.String())
	}
}
