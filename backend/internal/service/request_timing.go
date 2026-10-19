package service

import (
	"context"
	"sync"
	"time"
)

type requestTimingRecorderKey struct{}

// RequestTimingSnapshot contains request-scoped wait and upstream transport timings.
type RequestTimingSnapshot struct {
	AccountQueueWaitMs       *int64 `json:"account_queue_wait_ms,omitempty"`
	UpstreamConnectionWaitMs *int64 `json:"upstream_connection_wait_ms,omitempty"`
	UpstreamDNSMs           *int64 `json:"upstream_dns_ms,omitempty"`
	UpstreamConnectMs       *int64 `json:"upstream_connect_ms,omitempty"`
	UpstreamTLSMs           *int64 `json:"upstream_tls_ms,omitempty"`
	UpstreamRequestWriteMs  *int64 `json:"upstream_request_write_ms,omitempty"`
	UpstreamResponseWaitMs  *int64 `json:"upstream_response_wait_ms,omitempty"`
	UpstreamConnectionReused *bool  `json:"upstream_connection_reused,omitempty"`
}

// RequestTimingRecorder collects mutable timings for a single inbound request.
type RequestTimingRecorder struct {
	mu       sync.Mutex
	snapshot RequestTimingSnapshot
}

func NewRequestTimingRecorder() *RequestTimingRecorder {
	queueWaitMs := int64(0)
	return &RequestTimingRecorder{
		snapshot: RequestTimingSnapshot{AccountQueueWaitMs: &queueWaitMs},
	}
}

func WithRequestTimingRecorder(ctx context.Context, recorder *RequestTimingRecorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, requestTimingRecorderKey{}, recorder)
}

func RequestTimingRecorderFromContext(ctx context.Context) *RequestTimingRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(requestTimingRecorderKey{}).(*RequestTimingRecorder)
	return recorder
}

func (r *RequestTimingRecorder) AddAccountQueueWait(duration time.Duration) {
	if r == nil || duration < 0 {
		return
	}
	r.mu.Lock()
	addTimingMilliseconds(&r.snapshot.AccountQueueWaitMs, duration)
	r.mu.Unlock()
}

func (r *RequestTimingRecorder) BeginUpstreamAttempt() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.snapshot.UpstreamConnectionWaitMs = nil
	r.snapshot.UpstreamDNSMs = nil
	r.snapshot.UpstreamConnectMs = nil
	r.snapshot.UpstreamTLSMs = nil
	r.snapshot.UpstreamRequestWriteMs = nil
	r.snapshot.UpstreamResponseWaitMs = nil
	r.snapshot.UpstreamConnectionReused = nil
	r.mu.Unlock()
}

func (r *RequestTimingRecorder) SetUpstreamConnectionWait(duration time.Duration) {
	if r == nil {
		return
	}
	r.setUpstreamTiming(&r.snapshot.UpstreamConnectionWaitMs, duration)
}

func (r *RequestTimingRecorder) SetUpstreamDNS(duration time.Duration) {
	if r == nil {
		return
	}
	r.setUpstreamTiming(&r.snapshot.UpstreamDNSMs, duration)
}

func (r *RequestTimingRecorder) SetUpstreamConnect(duration time.Duration) {
	if r == nil {
		return
	}
	r.setUpstreamTiming(&r.snapshot.UpstreamConnectMs, duration)
}

func (r *RequestTimingRecorder) SetUpstreamTLS(duration time.Duration) {
	if r == nil {
		return
	}
	r.setUpstreamTiming(&r.snapshot.UpstreamTLSMs, duration)
}

func (r *RequestTimingRecorder) SetUpstreamRequestWrite(duration time.Duration) {
	if r == nil {
		return
	}
	r.setUpstreamTiming(&r.snapshot.UpstreamRequestWriteMs, duration)
}

func (r *RequestTimingRecorder) SetUpstreamResponseWait(duration time.Duration) {
	if r == nil {
		return
	}
	r.setUpstreamTiming(&r.snapshot.UpstreamResponseWaitMs, duration)
}

func (r *RequestTimingRecorder) SetUpstreamConnectionReused(reused bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.snapshot.UpstreamConnectionReused = &reused
	r.mu.Unlock()
}

func (r *RequestTimingRecorder) Snapshot() *RequestTimingSnapshot {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	snapshot := RequestTimingSnapshot{
		AccountQueueWaitMs:      cloneTimingInt64(r.snapshot.AccountQueueWaitMs),
		UpstreamConnectionWaitMs: cloneTimingInt64(r.snapshot.UpstreamConnectionWaitMs),
		UpstreamDNSMs:           cloneTimingInt64(r.snapshot.UpstreamDNSMs),
		UpstreamConnectMs:       cloneTimingInt64(r.snapshot.UpstreamConnectMs),
		UpstreamTLSMs:           cloneTimingInt64(r.snapshot.UpstreamTLSMs),
		UpstreamRequestWriteMs:  cloneTimingInt64(r.snapshot.UpstreamRequestWriteMs),
		UpstreamResponseWaitMs:  cloneTimingInt64(r.snapshot.UpstreamResponseWaitMs),
		UpstreamConnectionReused: cloneTimingBool(r.snapshot.UpstreamConnectionReused),
	}
	if snapshot.AccountQueueWaitMs == nil && snapshot.UpstreamConnectionWaitMs == nil && snapshot.UpstreamDNSMs == nil && snapshot.UpstreamConnectMs == nil &&
		snapshot.UpstreamTLSMs == nil && snapshot.UpstreamRequestWriteMs == nil && snapshot.UpstreamResponseWaitMs == nil &&
		snapshot.UpstreamConnectionReused == nil {
		return nil
	}
	return &snapshot
}

func addTimingMilliseconds(target **int64, duration time.Duration) {
	value := duration.Milliseconds()
	if *target != nil {
		value += **target
	}
	*target = &value
}

func (r *RequestTimingRecorder) setUpstreamTiming(target **int64, duration time.Duration) {
	if r == nil || duration < 0 {
		return
	}
	r.mu.Lock()
	*target = timingMilliseconds(duration)
	r.mu.Unlock()
}

func timingMilliseconds(duration time.Duration) *int64 {
	value := duration.Milliseconds()
	return &value
}

func cloneTimingInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTimingBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
