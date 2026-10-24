package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"
)

var degradationTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

const DefaultDegradationTestPrompt = "创建一个html,内容是svg绘制一个鹈鹕骑自行车的2D动画"

const (
	DegradationTestMaxResults         = 24
	DegradationTestDefaultResultLimit = 10
	DegradationTestMaxPromptLength    = 20000
)

// DegradationTestService provides CRUD operations for scheduled test plans and results.
type DegradationTestService struct {
	planRepo   DegradationTestPlanRepository
	resultRepo DegradationTestResultRepository
}

// NewDegradationTestService creates a new DegradationTestService.
func NewDegradationTestService(
	planRepo DegradationTestPlanRepository,
	resultRepo DegradationTestResultRepository,
) *DegradationTestService {
	return &DegradationTestService{
		planRepo:   planRepo,
		resultRepo: resultRepo,
	}
}

// CreatePlan validates the cron expression, computes next_run_at, and persists the plan.
func (s *DegradationTestService) CreatePlan(ctx context.Context, plan *DegradationTestPlan) (*DegradationTestPlan, error) {
	if plan != nil {
		// Retention is fixed; normalize values sent by older clients.
		plan.MaxResults = DegradationTestMaxResults
	}
	if err := validateDegradationTestPlan(plan); err != nil {
		return nil, err
	}
	nextRun, err := computeDegradationNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Create(ctx, plan)
}

// GetPlan retrieves a plan by ID.
func (s *DegradationTestService) GetPlan(ctx context.Context, id int64) (*DegradationTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *DegradationTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*DegradationTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// UpdatePlan validates cron and updates the plan.
func (s *DegradationTestService) UpdatePlan(ctx context.Context, plan *DegradationTestPlan) (*DegradationTestPlan, error) {
	if plan != nil {
		plan.MaxResults = DegradationTestMaxResults
	}
	if err := validateDegradationTestPlan(plan); err != nil {
		return nil, err
	}
	nextRun, err := computeDegradationNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *DegradationTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *DegradationTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*DegradationTestResult, error) {
	if limit <= 0 {
		limit = DegradationTestDefaultResultLimit
	} else if limit > DegradationTestMaxResults {
		limit = DegradationTestMaxResults
	}
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}

// SaveResult always keeps only the 24 latest executions, including for legacy plans.
// The retained argument preserves compatibility with existing runner call sites.
func (s *DegradationTestService) SaveResult(ctx context.Context, planID int64, _ int, result *DegradationTestResult) error {
	result.PlanID = planID
	saved, err := s.resultRepo.Create(ctx, result)
	if err != nil {
		return err
	}
	*result = *saved
	return s.resultRepo.PruneOldResults(ctx, planID, DegradationTestMaxResults)
}

func computeDegradationNextRun(cronExpr string, from time.Time) (time.Time, error) {
	cronExpr = strings.TrimSpace(cronExpr)
	if strings.HasPrefix(cronExpr, "@every ") {
		interval, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(cronExpr, "@every ")))
		if err != nil || interval < time.Minute || interval > 30*24*time.Hour {
			return time.Time{}, fmt.Errorf("interval must be between 1m and 720h (30 days)")
		}
		return from.Add(interval), nil
	}
	sched, err := degradationTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	next := sched.Next(from)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("schedule has no future execution")
	}
	return next, nil
}

func validateDegradationTestPlan(plan *DegradationTestPlan) error {
	if plan == nil || plan.AccountID <= 0 {
		return fmt.Errorf("account_id must be positive")
	}
	if utf8.RuneCountInString(plan.ModelID) > 100 {
		return fmt.Errorf("model_id must not exceed 100 characters")
	}
	if strings.TrimSpace(plan.ModelID) == "" {
		return fmt.Errorf("model_id is required")
	}
	if strings.TrimSpace(plan.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if utf8.RuneCountInString(plan.Prompt) > DegradationTestMaxPromptLength {
		return fmt.Errorf("prompt must not exceed %d characters", DegradationTestMaxPromptLength)
	}
	if len(plan.CronExpression) > 100 {
		return fmt.Errorf("schedule must not exceed 100 characters")
	}
	return nil
}
