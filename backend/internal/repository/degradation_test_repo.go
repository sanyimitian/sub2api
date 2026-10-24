package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// --- Plan Repository ---

type degradationTestPlanRepository struct {
	db *sql.DB
}

func NewDegradationTestPlanRepository(db *sql.DB) service.DegradationTestPlanRepository {
	return &degradationTestPlanRepository{db: db}
}

func (r *degradationTestPlanRepository) Create(ctx context.Context, plan *service.DegradationTestPlan) (*service.DegradationTestPlan, error) {
	plan.MaxResults = service.DegradationTestMaxResults
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO degradation_test_plans (account_id, model_id, cron_expression, enabled, max_results, next_run_at, prompt, reasoning_effort, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING id, account_id, model_id, cron_expression, enabled, max_results, last_run_at, next_run_at, created_at, updated_at, prompt, running_until, reasoning_effort
	`, plan.AccountID, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.NextRunAt, plan.Prompt, plan.ReasoningEffort)
	return scanDegradationPlan(row)
}

func (r *degradationTestPlanRepository) GetByID(ctx context.Context, id int64) (*service.DegradationTestPlan, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, model_id, cron_expression, enabled, max_results, last_run_at, next_run_at, created_at, updated_at, prompt, running_until, reasoning_effort
		FROM degradation_test_plans WHERE id = $1
	`, id)
	return scanDegradationPlan(row)
}

func (r *degradationTestPlanRepository) ListByAccountID(ctx context.Context, accountID int64) ([]*service.DegradationTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, model_id, cron_expression, enabled, max_results, last_run_at, next_run_at, created_at, updated_at, prompt, running_until, reasoning_effort
		FROM degradation_test_plans WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDegradationPlans(rows)
}

func (r *degradationTestPlanRepository) ListDue(ctx context.Context, now time.Time) ([]*service.DegradationTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, model_id, cron_expression, enabled, max_results, last_run_at, next_run_at, created_at, updated_at, prompt, running_until, reasoning_effort
		FROM degradation_test_plans
		WHERE enabled = true AND next_run_at <= $1
		AND (running_until IS NULL OR running_until <= $1)
		ORDER BY next_run_at ASC
	`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDegradationPlans(rows)
}

func (r *degradationTestPlanRepository) Update(ctx context.Context, plan *service.DegradationTestPlan) (*service.DegradationTestPlan, error) {
	plan.MaxResults = service.DegradationTestMaxResults
	row := r.db.QueryRowContext(ctx, `
		UPDATE degradation_test_plans
		SET model_id = $2, cron_expression = $3, enabled = $4, max_results = $5,
		    next_run_at = CASE WHEN cron_expression <> $3 OR (NOT enabled AND $4) THEN $6 ELSE next_run_at END,
		    prompt = $7, reasoning_effort = $8, updated_at = NOW()
		WHERE id = $1
		RETURNING id, account_id, model_id, cron_expression, enabled, max_results, last_run_at, next_run_at, created_at, updated_at, prompt, running_until, reasoning_effort
	`, plan.ID, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.NextRunAt, plan.Prompt, plan.ReasoningEffort)
	return scanDegradationPlan(row)
}

func (r *degradationTestPlanRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM degradation_test_plans WHERE id = $1`, id)
	return err
}

func (r *degradationTestPlanRepository) ClaimRun(ctx context.Context, id int64, scheduled bool, now, leaseUntil time.Time) (*service.DegradationTestPlan, error) {
	plan, err := scanDegradationPlan(r.db.QueryRowContext(ctx, `
		UPDATE degradation_test_plans SET running_until = $4
		WHERE id = $1 AND (running_until IS NULL OR running_until <= $3)
		AND (NOT $2 OR (enabled = true AND next_run_at <= $3))
		RETURNING id, account_id, model_id, cron_expression, enabled, max_results, last_run_at, next_run_at, created_at, updated_at, prompt, running_until, reasoning_effort
	`, id, scheduled, now, leaseUntil))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return plan, err
}

func (r *degradationTestPlanRepository) CompleteRun(ctx context.Context, plan *service.DegradationTestPlan, finishedAt time.Time, nextRunAt *time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE degradation_test_plans
		SET last_run_at = $2, running_until = NULL,
		    next_run_at = CASE WHEN cron_expression = $4 AND next_run_at IS NOT DISTINCT FROM $6::timestamptz
		        AND $3::timestamptz IS NOT NULL THEN $3 ELSE next_run_at END
		WHERE id = $1 AND running_until = $5
	`, plan.ID, finishedAt, nextRunAt, plan.CronExpression, plan.RunningUntil, plan.NextRunAt)
	return err
}

// --- Result Repository ---

type degradationTestResultRepository struct {
	db *sql.DB
}

func NewDegradationTestResultRepository(db *sql.DB) service.DegradationTestResultRepository {
	return &degradationTestResultRepository{db: db}
}

func (r *degradationTestResultRepository) Create(ctx context.Context, result *service.DegradationTestResult) (*service.DegradationTestResult, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO degradation_test_results (plan_id, status, response_text, error_message, latency_ms, started_at, finished_at, model_id, prompt, reasoning_effort, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		RETURNING id, plan_id, status, response_text, error_message, latency_ms, started_at, finished_at, created_at, model_id, prompt, reasoning_effort
	`, result.PlanID, result.Status, result.ResponseText, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt, result.ModelID, result.Prompt, result.ReasoningEffort)

	out := &service.DegradationTestResult{}
	if err := row.Scan(
		&out.ID, &out.PlanID, &out.Status, &out.ResponseText, &out.ErrorMessage,
		&out.LatencyMs, &out.StartedAt, &out.FinishedAt, &out.CreatedAt,
		&out.ModelID, &out.Prompt, &out.ReasoningEffort,
	); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *degradationTestResultRepository) ListByPlanID(ctx context.Context, planID int64, limit int) ([]*service.DegradationTestResult, error) {
	if limit <= 0 {
		limit = service.DegradationTestDefaultResultLimit
	} else if limit > service.DegradationTestMaxResults {
		limit = service.DegradationTestMaxResults
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, plan_id, status, response_text, error_message, latency_ms, started_at, finished_at, created_at, model_id, prompt, reasoning_effort
		FROM degradation_test_results
		WHERE plan_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, planID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	results := make([]*service.DegradationTestResult, 0)
	for rows.Next() {
		r := &service.DegradationTestResult{}
		if err := rows.Scan(
			&r.ID, &r.PlanID, &r.Status, &r.ResponseText, &r.ErrorMessage,
			&r.LatencyMs, &r.StartedAt, &r.FinishedAt, &r.CreatedAt,
			&r.ModelID, &r.Prompt, &r.ReasoningEffort,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (r *degradationTestResultRepository) PruneOldResults(ctx context.Context, planID int64, _ int) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM degradation_test_results
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY plan_id ORDER BY created_at DESC, id DESC) AS rn
				FROM degradation_test_results
				WHERE plan_id = $1
			) ranked
			WHERE rn > $2
		)
	`, planID, service.DegradationTestMaxResults)
	return err
}

// --- scan helpers ---

type degradationScannable interface {
	Scan(dest ...any) error
}

func scanDegradationPlan(row degradationScannable) (*service.DegradationTestPlan, error) {
	p := &service.DegradationTestPlan{}
	if err := row.Scan(
		&p.ID, &p.AccountID, &p.ModelID, &p.CronExpression, &p.Enabled, &p.MaxResults,
		&p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt,
		&p.Prompt, &p.RunningUntil, &p.ReasoningEffort,
	); err != nil {
		return nil, err
	}
	p.MaxResults = service.DegradationTestMaxResults
	return p, nil
}

func scanDegradationPlans(rows *sql.Rows) ([]*service.DegradationTestPlan, error) {
	plans := make([]*service.DegradationTestPlan, 0)
	for rows.Next() {
		p, err := scanDegradationPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}
