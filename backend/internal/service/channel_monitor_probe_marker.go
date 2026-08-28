package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ChannelMonitorProbeHeader is consumed at the gateway boundary. It must not
// be forwarded to an upstream provider.
const ChannelMonitorProbeHeader = "X-Sub2API-Channel-Monitor"

const (
	channelMonitorProbeMarkerTTL    = 5 * time.Minute
	channelMonitorProbeClockSkew    = 30 * time.Second
	channelMonitorProbeMaxRequestID = 128
)

type channelMonitorProbeMarkerPayload struct {
	MonitorID int64  `json:"monitor_id"`
	RequestID string `json:"request_id"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// ChannelMonitorProbe contains the trusted identity extracted from a marker.
// It is intentionally small so only monitor/request correlation crosses the
// gateway boundary.
type ChannelMonitorProbe struct {
	MonitorID int64
	RequestID string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type channelMonitorProbeContextKey struct{}

// ChannelMonitorProbeSigner signs and verifies internal probe markers. The
// key should be the deployment's existing JWT/security secret and must be
// shared by all gateway instances.
type ChannelMonitorProbeSigner struct {
	key []byte
	ttl time.Duration
}

func NewChannelMonitorProbeSigner(secret string) *ChannelMonitorProbeSigner {
	return &ChannelMonitorProbeSigner{key: []byte(strings.TrimSpace(secret)), ttl: channelMonitorProbeMarkerTTL}
}

func (s *ChannelMonitorProbeSigner) Sign(monitorID int64, requestID string, now time.Time) (string, error) {
	if s == nil || len(s.key) == 0 {
		return "", errors.New("channel monitor probe signer is not configured")
	}
	if monitorID <= 0 {
		return "", errors.New("monitor id must be positive")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || len(requestID) > channelMonitorProbeMaxRequestID {
		return "", errors.New("request id is invalid")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC().Truncate(time.Second)
	ttl := s.ttl
	if ttl <= 0 || ttl > channelMonitorProbeMarkerTTL {
		ttl = channelMonitorProbeMarkerTTL
	}
	payload := channelMonitorProbeMarkerPayload{
		MonitorID: monitorID,
		RequestID: requestID,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal probe marker: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded + "." + s.signature(encoded), nil
}

func (s *ChannelMonitorProbeSigner) signature(encodedPayload string) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(encodedPayload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *ChannelMonitorProbeSigner) Verify(marker string, now time.Time) (ChannelMonitorProbe, bool) {
	if s == nil || len(s.key) == 0 {
		return ChannelMonitorProbe{}, false
	}
	parts := strings.Split(strings.TrimSpace(marker), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ChannelMonitorProbe{}, false
	}
	expected := s.signature(parts[0])
	if _, err := hex.DecodeString(parts[1]); err != nil || !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return ChannelMonitorProbe{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ChannelMonitorProbe{}, false
	}
	var payload channelMonitorProbeMarkerPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.MonitorID <= 0 || payload.RequestID == "" || len(payload.RequestID) > channelMonitorProbeMaxRequestID {
		return ChannelMonitorProbe{}, false
	}
	if payload.ExpiresAt <= payload.IssuedAt {
		return ChannelMonitorProbe{}, false
	}
	if payload.ExpiresAt-payload.IssuedAt > int64(channelMonitorProbeMarkerTTL/time.Second) {
		return ChannelMonitorProbe{}, false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	issued := time.Unix(payload.IssuedAt, 0).UTC()
	expires := time.Unix(payload.ExpiresAt, 0).UTC()
	if now.Before(issued.Add(-channelMonitorProbeClockSkew)) || !now.Before(expires) {
		return ChannelMonitorProbe{}, false
	}
	return ChannelMonitorProbe{
		MonitorID: payload.MonitorID,
		RequestID: payload.RequestID,
		IssuedAt:  issued,
		ExpiresAt: expires,
	}, true
}

// WithChannelMonitorProbe attaches a trusted probe identity to a context.
func WithChannelMonitorProbe(ctx context.Context, probe ChannelMonitorProbe) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if probe.MonitorID <= 0 || strings.TrimSpace(probe.RequestID) == "" {
		return ctx
	}
	return context.WithValue(ctx, channelMonitorProbeContextKey{}, probe)
}

// ChannelMonitorProbeFromContext returns the trusted probe identity, if any.
func ChannelMonitorProbeFromContext(ctx context.Context) (ChannelMonitorProbe, bool) {
	if ctx == nil {
		return ChannelMonitorProbe{}, false
	}
	probe, ok := ctx.Value(channelMonitorProbeContextKey{}).(ChannelMonitorProbe)
	return probe, ok && probe.MonitorID > 0 && strings.TrimSpace(probe.RequestID) != ""
}

// ConsumeChannelMonitorProbeMarker verifies and consumes the marker from an
// inbound request. Header removal happens before verification so forged or
// expired values cannot leak to downstream providers either.
func ConsumeChannelMonitorProbeMarker(ctx context.Context, req *http.Request, signer *ChannelMonitorProbeSigner, now time.Time) (context.Context, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req == nil {
		return ctx, false
	}
	marker := req.Header.Get(ChannelMonitorProbeHeader)
	req.Header.Del(ChannelMonitorProbeHeader)
	probe, ok := signer.Verify(marker, now)
	if !ok {
		return ctx, false
	}
	return WithChannelMonitorProbe(ctx, probe), true
}
