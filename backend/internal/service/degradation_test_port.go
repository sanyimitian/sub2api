package service

import (
	"context"
	"time"
)

// DegradationTestPlan represents a scheduled test plan domain model.
type DegradationTestPlan struct {
	ID              int64      `json:"id"`
	AccountID       int64      `json:"account_id"`
	ModelID         string     `json:"model_id"`
	ReasoningEffort string     `json:"reasoning_effort"`
	Prompt          string     `json:"prompt"`
	CronExpression  string     `json:"cron_expression"`
	Enabled         bool       `json:"enabled"`
	MaxResults      int        `json:"max_results"`
	LastRunAt       *time.Time `json:"last_run_at"`
	NextRunAt       *time.Time `json:"next_run_at"`
	RunningUntil    *time.Time `json:"running_until,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// DegradationTestResult represents a single test execution result.
type DegradationTestResult struct {
	ID              int64     `json:"id"`
	PlanID          int64     `json:"plan_id"`
	ModelID         string    `json:"model_id"`
	ReasoningEffort string    `json:"reasoning_effort"`
	Prompt          string    `json:"prompt"`
	Status          string    `json:"status"`
	ResponseText    string    `json:"response_text"`
	ErrorMessage    string    `json:"error_message"`
	LatencyMs       int64     `json:"latency_ms"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	CreatedAt       time.Time `json:"created_at"`
}

// DegradationTestStatus keeps each plan visible even before its first execution.
// LatestResult is the latest execution, including failures, never a stale success.
type DegradationTestStatus struct {
	Plan            *DegradationTestPlan   `json:"plan"`
	AccountName     string                 `json:"account_name"`
	AccountPlatform string                 `json:"account_platform"`
	LatestResult    *DegradationTestResult `json:"latest_result"`
}

// DegradationTestPlanRepository defines the data access interface for test plans.
type DegradationTestPlanRepository interface {
	Create(ctx context.Context, plan *DegradationTestPlan) (*DegradationTestPlan, error)
	GetByID(ctx context.Context, id int64) (*DegradationTestPlan, error)
	ListByAccountID(ctx context.Context, accountID int64) ([]*DegradationTestPlan, error)
	ListStatus(ctx context.Context) ([]*DegradationTestStatus, error)
	ListDue(ctx context.Context, now time.Time) ([]*DegradationTestPlan, error)
	Update(ctx context.Context, plan *DegradationTestPlan) (*DegradationTestPlan, error)
	Delete(ctx context.Context, id int64) error
	// ClaimRun atomically leases a plan; scheduled claims additionally require a due, enabled plan.
	ClaimRun(ctx context.Context, id int64, scheduled bool, now, leaseUntil time.Time) (*DegradationTestPlan, error)
	// CompleteRun releases only this lease and preserves schedules edited during execution.
	// A nil nextRunAt records a manual run without changing the scheduled execution time.
	CompleteRun(ctx context.Context, plan *DegradationTestPlan, finishedAt time.Time, nextRunAt *time.Time) error
}

// DegradationTestResultRepository defines the data access interface for test results.
type DegradationTestResultRepository interface {
	Create(ctx context.Context, result *DegradationTestResult) (*DegradationTestResult, error)
	ListByPlanID(ctx context.Context, planID int64, limit int) ([]*DegradationTestResult, error)
	PruneOldResults(ctx context.Context, planID int64, keepCount int) error
}
