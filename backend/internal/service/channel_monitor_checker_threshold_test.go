//go:build unit

package service

import (
	"testing"
	"time"
)

func TestFinalizeOperationalOrDegradedUsesTwentySecondThreshold(t *testing.T) {
	tests := []struct {
		name       string
		latency    time.Duration
		wantStatus string
	}{
		{name: "below threshold remains operational", latency: 20*time.Second - time.Millisecond, wantStatus: MonitorStatusOperational},
		{name: "at threshold is degraded", latency: 20 * time.Second, wantStatus: MonitorStatusDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := finalizeOperationalOrDegraded(&CheckResult{}, tt.latency, int(tt.latency/time.Millisecond))
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tt.wantStatus)
			}
		})
	}
}
