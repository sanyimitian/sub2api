//go:build unit

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTokenInjectionRejectsInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ name, id, body string }{
		{"invalid id", "bad", `{"model_id":"gpt-test"}`},
		{"zero id", "0", `{"model_id":"gpt-test"}`},
		{"negative id", "-1", `{"model_id":"gpt-test"}`},
		{"missing model", "1", `{}`},
		{"blank model", "1", `{"model_id":"   "}`},
		{"invalid protocol", "1", `{"model_id":"gpt-test","protocol":"other"}`},
		{"long model", "1", `{"model_id":"` + strings.Repeat("x", 201) + `"}`},
		{"malformed json", "1", `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "id", Value: tc.id}}
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			// A nil service makes accidental dispatch on invalid input fail the test.
			(&AccountHandler{}).TestTokenInjection(c)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
