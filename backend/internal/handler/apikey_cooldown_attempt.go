package handler

import (
	"bytes"
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type apiKeyCooldownAttemptStarter interface {
	BeginAPIKeyCooldownAttempt(context.Context, *service.Account, string, bool, time.Time) (*service.APIKeyCooldownAttempt, bool, error)
}

func beginHandlerAPIKeyCooldownAttempt(ctx context.Context, starter apiKeyCooldownAttemptStarter, account *service.Account, model string, replaySafe bool) (context.Context, *service.APIKeyCooldownAttempt, bool, error) {
	if starter == nil {
		return ctx, nil, false, nil
	}
	attempt, blocked, err := starter.BeginAPIKeyCooldownAttempt(ctx, account, model, replaySafe, time.Now().UTC())
	if err != nil || blocked || attempt == nil {
		return ctx, attempt, blocked, err
	}
	return service.ContextWithAPIKeyCooldownAttempt(ctx, attempt), attempt, false, nil
}

type apiKeyCooldownAttemptResponseWriter struct {
	gin.ResponseWriter
	attempt *service.APIKeyCooldownAttempt
}

func (w *apiKeyCooldownAttemptResponseWriter) markStarted(data []byte) {
	if w != nil && w.attempt != nil {
		w.attempt.MarkResponseStarted()
		if apiKeyResponseChunkHasValidContent(data) {
			w.attempt.MarkValidContent()
		}
	}
}

func apiKeyResponseChunkHasValidContent(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return false
	}
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 && !bytes.HasPrefix(line, []byte(":")) {
			return true
		}
	}
	return false
}

func (w *apiKeyCooldownAttemptResponseWriter) WriteHeader(code int) {
	w.markStarted(nil)
	w.ResponseWriter.WriteHeader(code)
}

func (w *apiKeyCooldownAttemptResponseWriter) Write(data []byte) (int, error) {
	w.markStarted(data)
	return w.ResponseWriter.Write(data)
}

func (w *apiKeyCooldownAttemptResponseWriter) WriteString(data string) (int, error) {
	w.markStarted([]byte(data))
	return w.ResponseWriter.WriteString(data)
}

func (w *apiKeyCooldownAttemptResponseWriter) Flush() {
	w.markStarted(nil)
	w.ResponseWriter.Flush()
}

func installAPIKeyCooldownAttemptWriter(c *gin.Context, attempt *service.APIKeyCooldownAttempt) func() {
	if c == nil || c.Writer == nil || attempt == nil {
		return func() {}
	}
	original := c.Writer
	wrapped := &apiKeyCooldownAttemptResponseWriter{ResponseWriter: original, attempt: attempt}
	c.Writer = wrapped
	return func() {
		if c.Writer == wrapped {
			c.Writer = original
		}
	}
}
