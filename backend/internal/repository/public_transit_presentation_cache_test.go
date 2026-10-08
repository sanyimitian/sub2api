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

func TestPublicTransitPresentationCachePersistsWithoutExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	first := NewPublicTransitPresentationCache(client)
	state := service.PublicTransitPresentationState{
		SourceInput: 100, SourceRead: 400,
		Input: 60, Created: 60, Read: 880,
	}
	require.NoError(t, first.Set(context.Background(), "group/1/last_24h", state))

	server.FastForward(365 * 24 * time.Hour)
	second := NewPublicTransitPresentationCache(client)
	got, found, err := second.Get(context.Background(), "group/1/last_24h")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, state, got)
}
