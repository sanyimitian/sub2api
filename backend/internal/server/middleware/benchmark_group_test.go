//go:build unit

package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBenchmarkGroupBindingCannotChangeAuthenticatedGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7)
	for _, tc := range []struct {
		values  []string
		allowed bool
	}{
		{nil, true}, {[]string{"7"}, true}, {[]string{"8"}, false}, {[]string{"0"}, false}, {[]string{"7", "7"}, false}, {[]string{"7; DROP TABLE groups"}, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		for _, value := range tc.values {
			c.Request.Header.Add("X-Sub2API-Benchmark-Group-ID", value)
		}
		key := &service.APIKey{GroupID: &groupID}
		require.Equal(t, tc.allowed, validateBenchmarkGroupBinding(c, key))
		require.Equal(t, int64(7), *key.GroupID)
		require.Empty(t, c.Request.Header.Get("X-Sub2API-Benchmark-Group-ID"))
	}
}
