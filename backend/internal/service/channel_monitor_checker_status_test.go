package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFinalizeOperationalOrDegradedOnlyDegradesAboveTwelveSeconds(t *testing.T) {
	tests := []struct {
		name    string
		latency time.Duration
		want    string
	}{
		{name: "at threshold", latency: 12 * time.Second, want: MonitorStatusOperational},
		{name: "above threshold", latency: 12*time.Second + time.Millisecond, want: MonitorStatusDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := finalizeOperationalOrDegraded(&CheckResult{}, tt.latency, int(tt.latency/time.Millisecond))
			require.Equal(t, tt.want, res.Status)
		})
	}
}
