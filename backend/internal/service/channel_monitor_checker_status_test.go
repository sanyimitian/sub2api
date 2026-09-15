package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFinalizeOperationalOrDegraded_OnlyDegradesAboveTwentySeconds(t *testing.T) {
	tests := []struct {
		name       string
		latency    time.Duration
		wantStatus string
	}{
		{name: "below threshold", latency: 19*time.Second + 999*time.Millisecond, wantStatus: MonitorStatusOperational},
		{name: "at threshold", latency: 20 * time.Second, wantStatus: MonitorStatusOperational},
		{name: "above threshold", latency: 20*time.Second + time.Nanosecond, wantStatus: MonitorStatusDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := finalizeOperationalOrDegraded(&CheckResult{}, tt.latency, int(tt.latency/time.Millisecond))

			require.Equal(t, tt.wantStatus, result.Status)
		})
	}
}
