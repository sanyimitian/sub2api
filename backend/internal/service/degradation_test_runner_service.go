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
	degradationTestDefaultMaxWorkers  = 10
	degradationTestLeaseGrace         = time.Minute
	degradationTestPersistenceTimeout = 3 * time.Second
	degradationTestStopTimeout        = 8 * time.Second
)

var (
	ErrDegradationTestRunning  = errors.New("this test plan is already running")
	ErrDegradationTestCapacity = errors.New("all test execution slots are busy; try again shortly")
	ErrDegradationTestStopped  = errors.New("test runner is stopping; try again shortly")
)

type degradationAccountTester interface {
	RunGenerationBackground(context.Context, int64, string, string, ...string) (*DegradationTestResult, error)
}

// DegradationTestRunnerService periodically scans due test plans and executes them.
type DegradationTestRunnerService struct {
	planRepo       DegradationTestPlanRepository
	degradationSvc *DegradationTestService
	accountTestSvc degradationAccountTester
	cfg            *config.Config

	cron             *cron.Cron
	startOnce        sync.Once
	stopOnce         sync.Once
	workers          chan struct{}
	lifecycleMu      sync.Mutex
	stopping         bool
	backgroundCtx    context.Context
	cancelBackground context.CancelFunc
	workerWG         sync.WaitGroup
}

// NewDegradationTestRunnerService creates a new runner.
func NewDegradationTestRunnerService(
	planRepo DegradationTestPlanRepository,
	degradationSvc *DegradationTestService,
	accountTestSvc *AccountTestService,
	cfg *config.Config,
) *DegradationTestRunnerService {
	backgroundCtx, cancelBackground := context.WithCancel(context.Background())
	return &DegradationTestRunnerService{
		planRepo:         planRepo,
		degradationSvc:   degradationSvc,
		accountTestSvc:   accountTestSvc,
		cfg:              cfg,
		workers:          make(chan struct{}, degradationTestDefaultMaxWorkers),
		backgroundCtx:    backgroundCtx,
		cancelBackground: cancelBackground,
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
		// Serialize admission with WaitGroup.Add before waiting: no request can
		// start a worker after shutdown has begun waiting for the existing ones.
		s.lifecycleMu.Lock()
		s.stopping = true
		s.cancelBackground()
		s.lifecycleMu.Unlock()
		cronStopped := context.Background()
		if s.cron != nil {
			cronStopped = s.cron.Stop()
		}
		done := make(chan struct{})
		go func() {
			s.workerWG.Wait()
			if s.cron != nil {
				<-cronStopped.Done()
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(degradationTestStopTimeout):
			logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] worker shutdown timed out")
		}
	})
}

func (s *DegradationTestRunnerService) runScheduled() {
	ctx, cancel := context.WithTimeout(s.backgroundCtx, 10*time.Second)
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
		if err := s.reserveWorker(); err != nil {
			// Leave excess due plans for the next tick, without an unbounded goroutine queue.
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(p *DegradationTestPlan) {
			defer wg.Done()
			defer s.releaseWorker()
			if _, err := s.runOnePlan(context.Background(), p.ID, true); err != nil && !errors.Is(err, ErrDegradationTestRunning) {
				logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] plan=%d execution error: %v", p.ID, err)
			}
		}(plan)
	}

	wg.Wait()
}

// RunNow atomically claims a plan and returns immediately. Accepted work is
// owned by the service, so a closed HTTP request cannot cancel generation.
// Disabled plans are allowed; manual runs never advance the regular schedule.
func (s *DegradationTestRunnerService) RunNow(ctx context.Context, planID int64) (*DegradationTestPlan, error) {
	existing, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if existing.RunningUntil != nil && existing.RunningUntil.After(time.Now()) {
		return nil, ErrDegradationTestRunning
	}
	if err := s.reserveWorker(); err != nil {
		return nil, err
	}
	startedAt := time.Now()
	plan, err := s.claimPlan(ctx, planID, false, startedAt)
	if err != nil {
		s.releaseWorker()
		return nil, err
	}
	// HTTP serialization and background execution receive distinct snapshots.
	accepted := cloneDegradationTestPlan(plan)
	go func() {
		defer s.releaseWorker()
		if _, err := s.executeClaimedPlan(plan, false, startedAt); err != nil {
			logger.LegacyPrintf("service.degradation_test_runner", "[DegradationTestRunner] plan=%d execution error: %v", plan.ID, err)
		}
	}()
	return accepted, nil
}

func (s *DegradationTestRunnerService) reserveWorker() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopping {
		return ErrDegradationTestStopped
	}
	select {
	case s.workers <- struct{}{}:
		s.workerWG.Add(1)
		return nil
	default:
		return ErrDegradationTestCapacity
	}
}

func (s *DegradationTestRunnerService) releaseWorker() {
	<-s.workers
	s.workerWG.Done()
}

func cloneDegradationTestPlan(plan *DegradationTestPlan) *DegradationTestPlan {
	copy := *plan
	for _, pair := range [][2]**time.Time{{&copy.LastRunAt, &plan.LastRunAt}, {&copy.NextRunAt, &plan.NextRunAt}, {&copy.RunningUntil, &plan.RunningUntil}} {
		if *pair[1] != nil {
			value := **pair[1]
			*pair[0] = &value
		}
	}
	return &copy
}

func (s *DegradationTestRunnerService) claimPlan(parentCtx context.Context, planID int64, scheduled bool, startedAt time.Time) (*DegradationTestPlan, error) {
	claimCtx, claimCancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer claimCancel()
	stopCancellation := context.AfterFunc(s.backgroundCtx, claimCancel)
	defer stopCancellation()
	plan, err := s.planRepo.ClaimRun(claimCtx, planID, scheduled, startedAt)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, ErrDegradationTestRunning
	}
	return plan, nil
}

func (s *DegradationTestRunnerService) runOnePlan(parentCtx context.Context, planID int64, scheduled bool) (*DegradationTestResult, error) {
	startedAt := time.Now()
	plan, err := s.claimPlan(parentCtx, planID, scheduled, startedAt)
	if err != nil {
		return nil, err
	}
	return s.executeClaimedPlan(plan, scheduled, startedAt)
}

func (s *DegradationTestRunnerService) executeClaimedPlan(plan *DegradationTestPlan, scheduled bool, startedAt time.Time) (*DegradationTestResult, error) {
	timeoutSeconds := plan.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = DegradationTestDefaultTimeoutSeconds
	}
	runCtx, runCancel := context.WithDeadline(s.backgroundCtx, startedAt.Add(time.Duration(timeoutSeconds)*time.Second))
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
		if errors.Is(runErr, context.DeadlineExceeded) {
			result.ErrorMessage = fmt.Sprintf("Generation execution timed out after %d seconds: %v", timeoutSeconds, runErr)
		} else {
			result.ErrorMessage = runErr.Error()
		}
	}
	if result.Status != "success" && result.Status != "failed" {
		result.Status = "failed"
		if result.ErrorMessage == "" {
			result.ErrorMessage = "account test did not complete successfully"
		}
	}
	result.ModelID, result.Prompt = plan.ModelID, plan.Prompt
	result.ReasoningEffort = plan.ReasoningEffort
	result.StartedAt, result.FinishedAt = startedAt, finishedAt
	result.LatencyMs = finishedAt.Sub(startedAt).Milliseconds()

	// A disconnected HTTP client or an upstream timeout must not erase the execution result.
	persistCtx, persistCancel := context.WithTimeout(context.Background(), degradationTestPersistenceTimeout)
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
	completeCtx, completeCancel := context.WithTimeout(context.Background(), degradationTestPersistenceTimeout)
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
	return s.accountTestSvc.RunGenerationBackground(ctx, plan.AccountID, plan.ModelID, plan.Prompt, plan.ReasoningEffort)
}
