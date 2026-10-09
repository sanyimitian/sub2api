package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorFailuresMigration(t *testing.T) {
	content, err := FS.ReadFile("242_channel_monitor_failures_to_degraded.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "UPDATE channel_monitor_histories SET status = 'degraded'")
	require.Contains(t, sql, "WHERE status IN ('failed', 'error')")
}
