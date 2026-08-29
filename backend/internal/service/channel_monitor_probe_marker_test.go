package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorProbeMarkerRoundTripAndExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	signer := NewChannelMonitorProbeSigner("test-secret")
	marker, err := signer.Sign(42, "req-1", now)
	require.NoError(t, err)

	got, ok := signer.Verify(marker, now.Add(5*time.Second))
	require.True(t, ok)
	require.Equal(t, int64(42), got.MonitorID)
	require.Equal(t, "req-1", got.RequestID)

	_, ok = signer.Verify(marker, now.Add(channelMonitorProbeMarkerTTL+time.Second))
	require.False(t, ok)
}

func TestChannelMonitorProbeMarkerRejectsForgedAndMissing(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	signer := NewChannelMonitorProbeSigner("test-secret")
	marker, err := signer.Sign(42, "req-1", now)
	require.NoError(t, err)

	_, ok := signer.Verify(marker+"x", now)
	require.False(t, ok)
	_, ok = signer.Verify("", now)
	require.False(t, ok)
}

func TestChannelMonitorProbeMiddlewareInjectsContextAndRemovesHeader(t *testing.T) {
	signer := NewChannelMonitorProbeSigner("test-secret")
	now := time.Now().UTC()
	marker, err := signer.Sign(7, "req-7", now)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://example.test", nil)
	require.NoError(t, err)
	req.Header.Set(ChannelMonitorProbeHeader, marker)
	ctx, ok := ConsumeChannelMonitorProbeMarker(req.Context(), req, signer, now.Add(time.Second))
	require.True(t, ok)
	require.Empty(t, req.Header.Get(ChannelMonitorProbeHeader))
	probe, ok := ChannelMonitorProbeFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, int64(7), probe.MonitorID)
}
