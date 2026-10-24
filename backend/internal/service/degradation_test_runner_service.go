package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/robfig/cron/v3"
)

const (
	degradationTestDefaultMaxWorkers = 10
	degradationTestExecutionTimeout  = 4 * time.Minute
	degradationTestLeaseDuration     = 5 * time.Minute
)

var (
	ErrDegradationTestRunning  = errors.New("this test plan is already running")
	ErrDegradationTestCapacity = errors.New("all test execution slots are busy; try again shortly")
)

type degradationAccountTester interface {
	RunGenerationBackground(context.Context, int64, string, string) (*DegradationTestResult, error)
}

// DegradationTestRunnerService periodically scans due test plans and executes them.
type DegradationTestRunnerService struct {
	planRepo       DegradationTestPlanRepository
	degradationSvc *DegradationTestService
	accountTestSvc degradationAccountTester
	cfg            *config.Config

	cron      *cron.Cron
	startOnce sync.Once
	stopOnce  sync.Once
	workers   chan struct{}
}

// NewDegradationTestRunnerService creates a new runner.
func NewDegradationTestRunnerService(
	planRepo DegradationTestPlanRepository,
	degradationSvc *DegradationTestService,
	accountTestSvc *AccountTestService,
	cfg *config.Config,
) *DegradationTestRunnerService {
	return &DegradationTestRunnerService{
		planRepo:       planRepo,
		degradationSvc: degradationSvc,
		accountTestSvc: accountTestSvc,
		cfg:            cfg,
		workers:        make(chan struct{}, degradationTestDefaultMaxWorkers),
	}
}

// Start begins the cron ticker (every minute).
func (s *DegradationTestRunnerService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		loc := time.Local
		if s.cfg != nil {
			if parsed, err := time.LoadLocation(s.cfg.Timezone); err == nil && parsed != nil {
				loc = parsed
			}
		}

		c := cron.New(cron.WithParser(degradationTestCronParser), cron.WithLocation(loc))
		_, err := c.AddFunc("* * * * *", func() { s.runScheduled() })
		if err != nil {
			logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] not started (invalid schedule): %v", err)
			return
		}
		s.cron = c
		s.cron.Start()
		logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] started (tick=every minute)")
	})
}

// Stop gracefully shuts down the cron scheduler.
func (s *DegradationTestRunnerService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cron != nil {
			ctx := s.cron.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] cron stop timed out")
			}
		}
	})
}

func (s *DegradationTestRunnerService) runScheduled() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now()
	plans, err := s.planRepo.ListDue(ctx, now)
	if err != nil {
		logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] ListDue error: %v", err)
		return
	}
	if len(plans) == 0 {
		return
	}

	logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] found %d due plans", len(plans))

	var wg sync.WaitGroup

	for _, plan := range plans {
		select {
		case s.workers <- struct{}{}:
		default:
			// Leave excess due plans for the next tick, without an unbounded goroutine queue.
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(p *DegradationTestPlan) {
			defer wg.Done()
			defer func() { <-s.workers }()
			if _, err := s.runOnePlan(context.Background(), p.ID, true); err != nil && !errors.Is(err, ErrDegradationTestRunning) {
				logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] plan=%d execution error: %v", p.ID, err)
			}
		}(plan)
	}

	wg.Wait()
}

// RunNow also runs disabled plans and never advances their regular schedule.
func (s *DegradationTestRunnerService) RunNow(ctx context.Context, planID int64) (*DegradationTestResult, error) {
	if _, err := s.planRepo.GetByID(ctx, planID); err != nil {
		return nil, err
	}
	select {
	case s.workers <- struct{}{}:
		defer func() { <-s.workers }()
	default:
		return nil, ErrDegradationTestCapacity
	}
	return s.runOnePlan(ctx, planID, false)
}

func (s *DegradationTestRunnerService) runOnePlan(parentCtx context.Context, planID int64, scheduled bool) (*DegradationTestResult, error) {
	startedAt := time.Now()
	claimCtx, claimCancel := context.WithTimeout(parentCtx, 10*time.Second)
	plan, err := s.planRepo.ClaimRun(claimCtx, planID, scheduled, startedAt, startedAt.Add(degradationTestLeaseDuration))
	claimCancel()
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, ErrDegradationTestRunning
	}

	runCtx, runCancel := context.WithTimeout(parentCtx, degradationTestExecutionTimeout)
	result, runErr := s.invokeTest(runCtx, plan)
	if runErr == nil {
		runErr = runCtx.Err()
	}
	runCancel()
	finishedAt := time.Now()
	if result == nil {
		result = &DegradationTestResult{}
		if runErr == nil {
			runErr = errors.New("account test returned no result")
		}
	}
	if runErr != nil {
		result.Status = "failed"
		result.ErrorMessage = runErr.Error()
	}
	if result.Status != "success" && result.Status != "failed" {
		result.Status = "failed"
		if result.ErrorMessage == "" {
			result.ErrorMessage = "account test did not complete successfully"
		}
	}
	result.ModelID, result.Prompt = plan.ModelID, plan.Prompt
	result.StartedAt, result.FinishedAt = startedAt, finishedAt
	result.LatencyMs = finishedAt.Sub(startedAt).Milliseconds()

	// A disconnected HTTP client or an upstream timeout must not erase the execution result.
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 10*time.Second)
	saveErr := s.degradationSvc.SaveResult(persistCtx, plan.ID, plan.MaxResults, result)
	persistCancel()

	var nextRun *time.Time
	if scheduled {
		next, scheduleErr := computeDegradationNextRun(plan.CronExpression, finishedAt)
		if scheduleErr != nil {
			saveErr = errors.Join(saveErr, scheduleErr)
		} else {
			nextRun = &next
		}
	}
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	completeErr := s.planRepo.CompleteRun(completeCtx, plan, finishedAt, nextRun)
	completeCancel()
	if err := errors.Join(saveErr, completeErr); err != nil {
		return result, fmt.Errorf("persist test execution: %w", err)
	}

	return result, nil
}

func (s *DegradationTestRunnerService) invokeTest(ctx context.Context, plan *DegradationTestPlan) (result *DegradationTestResult, err error) {
	defer func() {
		if panicValue := recover(); panicValue != nil {
			err = fmt.Errorf("account test panicked: %v", panicValue)
		}
	}()
	return s.accountTestSvc.RunGenerationBackground(ctx, plan.AccountID, plan.ModelID, plan.Prompt)
}
