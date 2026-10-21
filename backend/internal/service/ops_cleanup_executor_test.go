package service

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDeleteOldOpenAIWSLatencySwitchLogs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	cutoff := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	query := regexp.QuoteMeta(`
WITH batch AS (
  SELECT id FROM ops_system_logs
  WHERE (message LIKE 'openai.ws_latency_switch_%'
    OR message IN ('openai.session_pool_assigned', 'openai.session_pool_assignment_completed', 'openai.session_pool_candidate_discarded'))
    AND created_at < $1
  ORDER BY id
  LIMIT $2
)
DELETE FROM ops_system_logs
WHERE id IN (SELECT id FROM batch)
`)
	mock.ExpectExec(query).
		WithArgs(cutoff, opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(query).
		WithArgs(cutoff, opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 0))

	deleted, err := deleteOldOpenAIWSLatencySwitchLogs(context.Background(), db, cutoff, opsCleanupBatchSize)
	require.NoError(t, err)
	require.EqualValues(t, 3, deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}
