package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var degradationPlanColumns = []string{
	"id", "account_id", "model_id", "cron_expression", "enabled", "max_results",
	"last_run_at", "next_run_at", "created_at", "updated_at", "prompt", "running_until",
}

func TestDegradationTestClaimGuardsLeaseAndDueState(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		now := time.Now()
		until := now.Add(5 * time.Minute)
		mock.ExpectQuery(`(?s)UPDATE degradation_test_plans SET running_until = \$4.*WHERE id = \$1 AND \(running_until IS NULL OR running_until <= \$3\).*AND \(NOT \$2 OR \(enabled = true AND next_run_at <= \$3\)\).*RETURNING`).
			WithArgs(int64(7), scheduled, now, until).
			WillReturnRows(sqlmock.NewRows(degradationPlanColumns).AddRow(7, 9, "model", "@every 30m", false, 50, nil, now, now, now, "prompt", until))
		plan, err := NewDegradationTestPlanRepository(db).ClaimRun(context.Background(), 7, scheduled, now, until)
		require.NoError(t, err)
		require.Equal(t, "prompt", plan.Prompt)
		require.Equal(t, until, *plan.RunningUntil)
		require.Equal(t, 24, plan.MaxResults)
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	}
}

func TestDegradationTestRepositoryResultLimitDefaultsToTenAndCapsAtTwentyFour(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	for _, tc := range []struct{ requested, want int }{{0, 10}, {3, 3}, {24, 24}, {50, 24}, {1000, 24}} {
		mock.ExpectQuery(`(?s)SELECT .* FROM degradation_test_results.*ORDER BY created_at DESC, id DESC.*LIMIT \$2`).
			WithArgs(int64(7), tc.want).
			WillReturnRows(sqlmock.NewRows([]string{"id", "plan_id", "status", "response_text", "error_message", "latency_ms", "started_at", "finished_at", "created_at", "model_id", "prompt"}))
		results, err := NewDegradationTestResultRepository(db).ListByPlanID(context.Background(), 7, tc.requested)
		require.NoError(t, err)
		require.Empty(t, results)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDegradationTestBusyClaimReturnsNoPlan(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now()
	mock.ExpectQuery(`UPDATE degradation_test_plans SET running_until`).WillReturnRows(sqlmock.NewRows(degradationPlanColumns))
	plan, err := NewDegradationTestPlanRepository(db).ClaimRun(context.Background(), 7, false, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.Nil(t, plan)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDegradationTestCompletionRequiresLeaseAndPreservesConcurrentEdits(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now()
	until := now.Add(5 * time.Minute)
	previousNext := now.Add(-time.Minute)
	plan := &service.DegradationTestPlan{ID: 7, UpdatedAt: now.Add(-time.Minute), CronExpression: "@every 30m", NextRunAt: &previousNext, RunningUntil: &until}
	next := now.Add(30 * time.Minute)
	// Schedule comparison deliberately excludes updated_at: prompt/model/retention edits
	// must still advance this occurrence, while a changed cron or next_run_at must survive.
	query := `(?s)UPDATE degradation_test_plans.*SET last_run_at = \$2, running_until = NULL,.*next_run_at = CASE WHEN cron_expression = \$4 AND next_run_at IS NOT DISTINCT FROM \$6::timestamptz.*AND \$3::timestamptz IS NOT NULL THEN \$3 ELSE next_run_at END.*WHERE id = \$1 AND running_until = \$5`
	mock.ExpectExec(query).WithArgs(plan.ID, now, next, plan.CronExpression, until, previousNext).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, NewDegradationTestPlanRepository(db).CompleteRun(context.Background(), plan, now, &next))
	// A manual run passes NULL and cannot reschedule an existing timer.
	mock.ExpectExec(query).WithArgs(plan.ID, now, nil, plan.CronExpression, until, previousNext).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, NewDegradationTestPlanRepository(db).CompleteRun(context.Background(), plan, now, nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDegradationTestResultStoresPromptAndModelSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	now := time.Now()
	result := &service.DegradationTestResult{PlanID: 7, ModelID: "chosen-model", Prompt: "original prompt", Status: "failed", ErrorMessage: "upstream timeout", StartedAt: now, FinishedAt: now}
	mock.ExpectQuery(`(?s)INSERT INTO degradation_test_results .*model_id, prompt, created_at.*RETURNING`).
		WithArgs(result.PlanID, result.Status, result.ResponseText, result.ErrorMessage, result.LatencyMs, now, now, result.ModelID, result.Prompt).
		WillReturnRows(sqlmock.NewRows([]string{"id", "plan_id", "status", "response_text", "error_message", "latency_ms", "started_at", "finished_at", "created_at", "model_id", "prompt"}).
			AddRow(12, 7, "failed", "", "upstream timeout", 0, now, now, now, "chosen-model", "original prompt"))
	saved, err := NewDegradationTestResultRepository(db).Create(context.Background(), result)
	require.NoError(t, err)
	require.EqualValues(t, 12, saved.ID)
	require.Equal(t, result.ModelID, saved.ModelID)
	require.Equal(t, result.Prompt, saved.Prompt)
	require.NoError(t, mock.ExpectationsWereMet())
}
