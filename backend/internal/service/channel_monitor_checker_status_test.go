package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFinalizeOperationalOrDegradedUsesTwentySecondThreshold(t *testing.T) {
	tests := []struct {
		name    string
		latency time.Duration
		want    string
	}{
		{name: "below threshold", latency: 20*time.Second - time.Millisecond, want: MonitorStatusOperational},
		{name: "at threshold", latency: 20 * time.Second, want: MonitorStatusDegraded},
		{name: "above threshold", latency: 20*time.Second + time.Millisecond, want: MonitorStatusDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := finalizeOperationalOrDegraded(&CheckResult{}, tt.latency, int(tt.latency/time.Millisecond))
			require.Equal(t, tt.want, res.Status)
		})
	}
}

func TestMonitorRequestErrorStatus(t *testing.T) {
	require.Equal(t, MonitorStatusFailed, monitorRequestErrorStatus(context.DeadlineExceeded))
	require.Equal(t, MonitorStatusError, monitorRequestErrorStatus(context.Canceled))
	require.Equal(t, MonitorStatusError, monitorRequestErrorStatus(errors.New("connection reset")))
}
