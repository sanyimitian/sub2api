package middleware

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// This optional assertion can only restrict a normal authenticated request; it
// never selects or grants access to a group. Rebinding a scheduled test's key
// must fail rather than silently benchmark another group under the old label.
func validateBenchmarkGroupBinding(c *gin.Context, key *service.APIKey) bool {
	const header = "X-Sub2API-Benchmark-Group-ID"
	values := c.Request.Header.Values(header)
	if len(values) == 0 {
		return true
	}
	c.Request.Header.Del(header)
	if len(values) == 1 {
		id, err := strconv.ParseInt(values[0], 10, 64)
		if err == nil && id > 0 && key != nil && key.GroupID != nil && *key.GroupID == id {
			return true
		}
	}
	AbortWithError(c, http.StatusConflict, "BENCHMARK_GROUP_CHANGED", "The benchmark API key no longer matches the selected group")
	return false
}
