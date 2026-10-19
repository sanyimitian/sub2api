package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSubmitUsageRecordTaskCopiesRequestContext(t *testing.T) {
	recorder := service.NewRequestTimingRecorder()
	recorder.AddAccountQueueWait(123)
	parent := service.WithRequestTimingRecorder(context.Background(), recorder)
	parent = context.WithValue(parent, ctxkey.ClientRequestID, "client-request-123")
	parent = context.WithValue(parent, ctxkey.RequestID, "request-456")

	var gotClientRequestID string
	var gotRequestID string
	var gotTimings *service.RequestTimingSnapshot
	h := &GatewayHandler{}
	h.submitUsageRecordTask(parent, func(ctx context.Context) {
		gotClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		gotRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
		gotTimings = service.RequestTimingRecorderFromContext(ctx).Snapshot()
	})

	require.Equal(t, "client-request-123", gotClientRequestID)
	require.Equal(t, "request-456", gotRequestID)
	require.NotNil(t, gotTimings)
	require.NotNil(t, gotTimings.AccountQueueWaitMs)
	require.Equal(t, int64(123), *gotTimings.AccountQueueWaitMs)
}

func TestOpenAISubmitUsageRecordTaskCopiesRequestContext(t *testing.T) {
	recorder := service.NewRequestTimingRecorder()
	recorder.AddAccountQueueWait(456)
	parent := service.WithRequestTimingRecorder(context.Background(), recorder)
	parent = context.WithValue(parent, ctxkey.ClientRequestID, "openai-client-request-123")
	parent = context.WithValue(parent, ctxkey.RequestID, "openai-request-456")

	var gotClientRequestID string
	var gotRequestID string
	var gotTimings *service.RequestTimingSnapshot
	h := &OpenAIGatewayHandler{}
	h.submitUsageRecordTask(parent, func(ctx context.Context) {
		gotClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
		gotRequestID, _ = ctx.Value(ctxkey.RequestID).(string)
		gotTimings = service.RequestTimingRecorderFromContext(ctx).Snapshot()
	})

	require.Equal(t, "openai-client-request-123", gotClientRequestID)
	require.Equal(t, "openai-request-456", gotRequestID)
	require.NotNil(t, gotTimings)
	require.NotNil(t, gotTimings.AccountQueueWaitMs)
	require.Equal(t, int64(456), *gotTimings.AccountQueueWaitMs)
}
