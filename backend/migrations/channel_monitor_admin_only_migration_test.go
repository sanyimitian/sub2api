package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorAdminOnlyMigrationPreservesExistingVisibility(t *testing.T) {
	content, err := FS.ReadFile("247_channel_monitor_admin_only.sql")
	require.NoError(t, err)
	statement := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, statement, "ALTER TABLE channel_monitors ADD COLUMN IF NOT EXISTS admin_only BOOLEAN NOT NULL DEFAULT false")
	require.NotContains(t, statement, "UPDATE")
}
