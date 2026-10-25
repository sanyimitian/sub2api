//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func degradationStatusDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE accounts (id INTEGER PRIMARY KEY, name TEXT NOT NULL, platform TEXT NOT NULL, deleted_at TIMESTAMP);
		CREATE TABLE degradation_test_plans (
		    id INTEGER PRIMARY KEY, account_id INTEGER NOT NULL, model_id TEXT NOT NULL,
		    cron_expression TEXT NOT NULL, enabled BOOLEAN NOT NULL, max_results INTEGER NOT NULL DEFAULT 24,
		    last_run_at TIMESTAMP, next_run_at TIMESTAMP, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
		    prompt TEXT NOT NULL, running_until TIMESTAMP, reasoning_effort TEXT NOT NULL DEFAULT '', timeout_seconds INTEGER NOT NULL DEFAULT 600
		);
		CREATE TABLE degradation_test_results (
		    id INTEGER PRIMARY KEY, plan_id INTEGER NOT NULL, model_id TEXT NOT NULL,
		    prompt TEXT NOT NULL, reasoning_effort TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
		    response_text TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '', latency_ms INTEGER NOT NULL DEFAULT 0,
		    started_at TIMESTAMP NOT NULL, finished_at TIMESTAMP NOT NULL, created_at TIMESTAMP NOT NULL
		);
		CREATE INDEX idx_dtr_plan_created ON degradation_test_results(plan_id, created_at DESC, id DESC);
		CREATE TABLE scheduled_test_plans (id INTEGER PRIMARY KEY, account_id INTEGER NOT NULL);
		INSERT INTO scheduled_test_plans VALUES (999, 1)`)
	require.NoError(t, err)
	return db
}

func TestDegradationTestStatusIncludesEveryPlanAndLatestFailure(t *testing.T) {
	db := degradationStatusDB(t)
	now := time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC)
	_, err := db.Exec(`INSERT INTO accounts (id, name, platform, deleted_at) VALUES
		(1, 'xzp-0.12', 'openai', NULL), (2, 'deleted', 'anthropic', ?), (3, 'no plans', 'gemini', NULL)`, now)
	require.NoError(t, err)
	for _, p := range []struct {
		id, accountID int
		model         string
		enabled       bool
	}{{1, 1, "model-a", true}, {2, 1, "model-b", false}, {3, 1, "model-a", true}, {4, 2, "deleted-model", true}} {
		_, err = db.Exec(`INSERT INTO degradation_test_plans
			(id, account_id, model_id, cron_expression, enabled, created_at, updated_at, prompt, reasoning_effort)
			VALUES (?, ?, ?, '@every 30m', ?, ?, ?, 'current prompt', 'xhigh')`, p.id, p.accountID, p.model, p.enabled, now, now)
		require.NoError(t, err)
	}
	for _, result := range []struct {
		id, planID int
		status     string
		createdAt  time.Time
	}{
		{10, 1, "success", now.Add(-time.Minute)},
		{11, 1, "success", now},
		{12, 1, "failed", now}, // Tied timestamps must select the larger ID, including failures.
		{13, 2, "success", now},
		{14, 4, "success", now},
	} {
		_, err = db.Exec(`INSERT INTO degradation_test_results
			(id, plan_id, model_id, prompt, reasoning_effort, status, response_text, error_message, latency_ms, started_at, finished_at, created_at)
			VALUES (?, ?, 'original-model', 'original prompt', 'high', ?, '<svg/>', 'upstream failed', 42, ?, ?, ?)`,
			result.id, result.planID, result.status, now.Add(-2*time.Minute), now, result.createdAt)
		require.NoError(t, err)
	}
	statuses, err := NewDegradationTestPlanRepository(db).ListStatus(context.Background())
	require.NoError(t, err)
	require.Len(t, statuses, 3)
	byID := make(map[int64]*service.DegradationTestStatus)
	for _, status := range statuses {
		byID[status.Plan.ID] = status
		require.Equal(t, "xzp-0.12", status.AccountName)
		require.Equal(t, "openai", status.AccountPlatform)
		require.Equal(t, "xhigh", status.Plan.ReasoningEffort)
	}
	require.NotContains(t, byID, int64(4))
	require.NotContains(t, byID, int64(999))
	require.EqualValues(t, 12, byID[1].LatestResult.ID)
	require.Equal(t, "failed", byID[1].LatestResult.Status)
	require.Equal(t, "upstream failed", byID[1].LatestResult.ErrorMessage)
	require.Equal(t, "<svg/>", byID[1].LatestResult.ResponseText)
	require.Equal(t, "original-model", byID[1].LatestResult.ModelID)
	require.Equal(t, "original prompt", byID[1].LatestResult.Prompt)
	require.Equal(t, "high", byID[1].LatestResult.ReasoningEffort)
	require.EqualValues(t, 42, byID[1].LatestResult.LatencyMs)
	require.True(t, now.Equal(byID[1].LatestResult.CreatedAt))
	require.False(t, byID[2].Plan.Enabled)
	require.Equal(t, "success", byID[2].LatestResult.Status)
	require.Nil(t, byID[3].LatestResult)
	require.Nil(t, byID[3].Plan.LastRunAt)
}

func TestDegradationTestStatusEmptyReturnsArrayAndUsesOneQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	// A second query would fail: account and latest-result lookup are joined.
	mock.ExpectQuery(`(?s)SELECT p.id.*JOIN accounts AS a.*a.deleted_at IS NULL.*LEFT JOIN degradation_test_results.*ORDER BY candidate.created_at DESC, candidate.id DESC LIMIT 1`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	statuses, err := NewDegradationTestPlanRepository(db).ListStatus(context.Background())
	require.NoError(t, err)
	require.NotNil(t, statuses)
	require.Empty(t, statuses)
	require.NoError(t, mock.ExpectationsWereMet())
}
