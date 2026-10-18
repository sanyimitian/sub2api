package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSLatencyCacheCASAcrossInstancesAndExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first := NewGatewayCache(client).(service.OpenAIWSLatencyCache)
	second := NewGatewayCache(client).(service.OpenAIWSLatencyCache)
	ctx := context.Background()
	initial := []byte(`{"slow_count":1}`)
	next := []byte(`{"slow_count":2}`)
	ok, err := first.CompareAndSwapOpenAIWSLatencyState(ctx, "thread-1", nil, initial, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	loaded, err := second.GetOpenAIWSLatencyState(ctx, "thread-1")
	require.NoError(t, err)
	require.Equal(t, initial, loaded)
	ok, err = second.CompareAndSwapOpenAIWSLatencyState(ctx, "thread-1", nil, next, time.Minute)
	require.NoError(t, err)
	require.False(t, ok, "stale writers must not overwrite a newer state")
	ok, err = second.CompareAndSwapOpenAIWSLatencyState(ctx, "thread-1", loaded, next, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	server.FastForward(time.Minute + time.Second)
	loaded, err = first.GetOpenAIWSLatencyState(ctx, "thread-1")
	require.NoError(t, err)
	require.Empty(t, loaded)
}
