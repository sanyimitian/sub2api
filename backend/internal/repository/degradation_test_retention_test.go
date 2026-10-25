//go:build unit

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// The retention DML is portable SQL; execute it against real rows to verify
// partitioning, mixed statuses and deterministic ordering for timestamp ties.
func degradationRetentionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE degradation_test_plans (id INTEGER PRIMARY KEY, max_results INTEGER NOT NULL);
		CREATE TABLE degradation_test_results (id INTEGER PRIMARY KEY, plan_id INTEGER NOT NULL, created_at INTEGER NOT NULL, status TEXT NOT NULL);
		INSERT INTO degradation_test_plans (id, max_results) VALUES (1, 50), (2, 1), (3, 1000)`)
	require.NoError(t, err)
	for planID, count := range map[int]int{1: 30, 2: 27, 3: 3} {
		for i := 1; i <= count; i++ {
			id := (planID-1)*100 + i
			createdAt := 200
			if id == 1 || id == 127 {
				createdAt = 100
			}
			status := "success"
			if i%2 == 0 {
				status = "failed"
			}
			_, err := db.Exec("INSERT INTO degradation_test_results (id, plan_id, created_at, status) VALUES (?, ?, ?, ?)", id, planID, createdAt, status)
			require.NoError(t, err)
		}
	}
	return db
}

func retainedDegradationIDs(t *testing.T, db *sql.DB, planID int) []int {
	t.Helper()
	rows, err := db.Query("SELECT id FROM degradation_test_results WHERE plan_id = ? ORDER BY id", planID)
	require.NoError(t, err)
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

func degradationIDRange(first, last int) []int {
	ids := make([]int, 0, last-first+1)
	for id := first; id <= last; id++ {
		ids = append(ids, id)
	}
	return ids
}

func TestDegradationTestRepositoryPrunesOnlyLatestTwentyFourPerPlan(t *testing.T) {
	db := degradationRetentionDB(t)
	// Identical plan IDs in the original feature must not be touched by pruning.
	_, err := db.Exec(`CREATE TABLE scheduled_test_results (id INTEGER PRIMARY KEY, plan_id INTEGER, response_text TEXT);
		INSERT INTO scheduled_test_results VALUES (1, 1, 'original health check')`)
	require.NoError(t, err)
	repo := NewDegradationTestResultRepository(db)
	for range 2 {
		require.NoError(t, repo.PruneOldResults(context.Background(), 1, 1000))
		require.Equal(t, degradationIDRange(7, 30), retainedDegradationIDs(t, db, 1))
	}
	require.Len(t, retainedDegradationIDs(t, db, 2), 27)
	require.NoError(t, repo.PruneOldResults(context.Background(), 2, 0))
	require.Equal(t, degradationIDRange(103, 126), retainedDegradationIDs(t, db, 2))
	require.Equal(t, []int{201, 202, 203}, retainedDegradationIDs(t, db, 3))
	var original string
	require.NoError(t, db.QueryRow("SELECT response_text FROM scheduled_test_results WHERE id = 1").Scan(&original))
	require.Equal(t, "original health check", original)
}

func TestDegradationTestMigrationCreatesIndependentTablesAndCascades(t *testing.T) {
	content, err := dbmigrations.FS.ReadFile("245_create_degradation_tests.sql")
	require.NoError(t, err)
	migration := string(content)
	require.NotContains(t, migration, "scheduled_test_plans")
	require.NotContains(t, migration, "scheduled_test_results")
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`PRAGMA foreign_keys = ON;
		CREATE TABLE accounts (id INTEGER PRIMARY KEY);
		INSERT INTO accounts VALUES (7);
		CREATE TABLE scheduled_test_plans (id INTEGER PRIMARY KEY, account_id INTEGER, max_results INTEGER, auto_recover INTEGER);
		CREATE TABLE scheduled_test_results (id INTEGER PRIMARY KEY, plan_id INTEGER, response_text TEXT);
		INSERT INTO scheduled_test_plans VALUES (1, 7, 50, 1);
		INSERT INTO scheduled_test_results VALUES (1, 1, 'original result')`)
	require.NoError(t, err)
	// Only dialect-specific auto IDs and current-time defaults differ in SQLite.
	migration = strings.ReplaceAll(migration, "BIGSERIAL PRIMARY KEY", "INTEGER PRIMARY KEY")
	migration = strings.ReplaceAll(migration, "NOW()", "CURRENT_TIMESTAMP")
	_, err = db.Exec(migration)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO degradation_test_plans (id, account_id, model_id, prompt) VALUES (1, 7, 'chosen-model', 'generate SVG');
		INSERT INTO degradation_test_results (id, plan_id, model_id, prompt, response_text) VALUES (1, 1, 'chosen-model', 'generate SVG', '<svg/>')`)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE degradation_test_plans SET max_results = 50 WHERE id = 1")
	require.Error(t, err)
	_, err = db.Exec("DELETE FROM degradation_test_plans WHERE id = 1")
	require.NoError(t, err)
	var count, retention, recovery int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM degradation_test_results").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow("SELECT max_results, auto_recover FROM scheduled_test_plans WHERE id = 1").Scan(&retention, &recovery))
	require.Equal(t, 50, retention)
	require.Equal(t, 1, recovery)
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM scheduled_test_results").Scan(&count))
	require.Equal(t, 1, count)
}

func TestDegradationTestReasoningEffortMigrationPreservesDefaultsAndHistory(t *testing.T) {
	content, err := dbmigrations.FS.ReadFile("246_degradation_test_reasoning_effort.sql")
	require.NoError(t, err)
	migration := string(content)
	require.NotContains(t, migration, "scheduled_test_")
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE degradation_test_plans (id INTEGER PRIMARY KEY, model_id TEXT NOT NULL);
		CREATE TABLE degradation_test_results (id INTEGER PRIMARY KEY, plan_id INTEGER NOT NULL, response_text TEXT NOT NULL);
		INSERT INTO degradation_test_plans VALUES (1, 'gpt-5');
		INSERT INTO degradation_test_results VALUES (1, 1, '<svg/>')`)
	require.NoError(t, err)
	// SQLite supports the same default/check semantics without this PG guard.
	migration = strings.ReplaceAll(migration, "ADD COLUMN IF NOT EXISTS", "ADD COLUMN")
	_, err = db.Exec(migration)
	require.NoError(t, err)
	var model, text, effort string
	require.NoError(t, db.QueryRow("SELECT model_id, reasoning_effort FROM degradation_test_plans WHERE id=1").Scan(&model, &effort))
	require.Equal(t, "gpt-5", model)
	require.Empty(t, effort)
	require.NoError(t, db.QueryRow("SELECT response_text, reasoning_effort FROM degradation_test_results WHERE id=1").Scan(&text, &effort))
	require.Equal(t, "<svg/>", text)
	require.Empty(t, effort)
	_, err = db.Exec("UPDATE degradation_test_plans SET reasoning_effort='xhigh' WHERE id=1")
	require.NoError(t, err)
	// Updating a plan cannot rewrite the effort of an earlier execution.
	require.NoError(t, db.QueryRow("SELECT reasoning_effort FROM degradation_test_results WHERE id=1").Scan(&effort))
	require.Empty(t, effort)
	_, err = db.Exec("UPDATE degradation_test_plans SET reasoning_effort='extreme' WHERE id=1")
	require.Error(t, err)
	_, err = db.Exec("INSERT INTO degradation_test_results (id, plan_id, response_text, reasoning_effort) VALUES (2, 1, '<html/>', 'high')")
	require.NoError(t, err)
	require.NoError(t, db.QueryRow("SELECT reasoning_effort FROM degradation_test_results WHERE id=2").Scan(&effort))
	require.Equal(t, "high", effort)
}

func TestDegradationTestTimeoutMigrationPreservesPlansWithTenMinuteDefault(t *testing.T) {
	content, err := dbmigrations.FS.ReadFile("248_degradation_test_timeout.sql")
	require.NoError(t, err)
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE degradation_test_plans (id INTEGER PRIMARY KEY, model_id TEXT NOT NULL);
		INSERT INTO degradation_test_plans VALUES (1, 'existing-model')`)
	require.NoError(t, err)
	_, err = db.Exec(strings.ReplaceAll(string(content), "ADD COLUMN IF NOT EXISTS", "ADD COLUMN"))
	require.NoError(t, err)
	var seconds int
	var model string
	require.NoError(t, db.QueryRow("SELECT model_id, timeout_seconds FROM degradation_test_plans WHERE id=1").Scan(&model, &seconds))
	require.Equal(t, "existing-model", model)
	require.Equal(t, 600, seconds)
	_, err = db.Exec("UPDATE degradation_test_plans SET timeout_seconds=59 WHERE id=1")
	require.Error(t, err)
	_, err = db.Exec("UPDATE degradation_test_plans SET timeout_seconds=1801 WHERE id=1")
	require.Error(t, err)
	_, err = db.Exec("UPDATE degradation_test_plans SET timeout_seconds=1800 WHERE id=1")
	require.NoError(t, err)
}
