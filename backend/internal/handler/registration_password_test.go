package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegistrationHandlersRejectWeakPasswordBeforeCaptchaOrSession(t *testing.T) {
	h := &AuthHandler{}
	for name, handle := range map[string]func(*gin.Context){
		"email":          h.Register,
		"pending oauth":  h.CreatePendingOAuthAccount,
		"verified oauth": func(c *gin.Context) { h.completeEmailOAuthRegistration(c, "google") },
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"email":"fresh@example.com","password":"12345678","verify_code":"123456"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			handle(c)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "PASSWORD_TOO_WEAK")
		})
	}
}
