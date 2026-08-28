package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apiKeyCooldownAttemptStarterStub struct {
	attempt *service.APIKeyCooldownAttempt
	blocked bool
	err     error
}

func (s apiKeyCooldownAttemptStarterStub) BeginAPIKeyCooldownAttempt(context.Context, *service.Account, string, bool, time.Time) (*service.APIKeyCooldownAttempt, bool, error) {
	return s.attempt, s.blocked, s.err
}

func TestBeginHandlerAPIKeyCooldownAttemptAddsAttemptToContext(t *testing.T) {
	attempt := &service.APIKeyCooldownAttempt{ID: "attempt-1"}
	ctx, got, blocked, err := beginHandlerAPIKeyCooldownAttempt(
		context.Background(), apiKeyCooldownAttemptStarterStub{attempt: attempt},
		&service.Account{ID: 42}, "gpt-5", true,
	)
	require.NoError(t, err)
	require.False(t, blocked)
	require.Same(t, attempt, got)
	require.Same(t, attempt, service.APIKeyCooldownAttemptFromContext(ctx))
}

func TestInstallAPIKeyCooldownAttemptWriterMarksAndRestoresWriter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	original := c.Writer
	attempt := &service.APIKeyCooldownAttempt{ID: "attempt-2"}
	restore := installAPIKeyCooldownAttemptWriter(c, attempt)

	c.Writer.WriteHeader(200)
	require.True(t, attempt.ResponseStarted())
	restore()
	require.Same(t, original, c.Writer)
}
