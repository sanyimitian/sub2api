package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func awaitDegradationWorkers(t *testing.T, runner *DegradationTestRunnerService) {
	t.Helper()
	done := make(chan struct{})
	go func() { runner.workerWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("background generation did not finish")
	}
}

func TestDegradationTestAsyncRunSurvivesRequestCancellationAndKeepsSchedule(t *testing.T) {
	entered := make(chan context.Context, 1)
	finish := make(chan struct{})
	runner, plans, results := newDegradationRunnerTest(t, func(ctx context.Context, _ int64, _ string, _ string) (*DegradationTestResult, error) {
		entered <- ctx
		select {
		case <-finish:
			return &DegradationTestResult{Status: "success", ResponseText: "<svg/>"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	accepted, err := runner.RunNow(requestCtx, 7)
	require.NoError(t, err)
	require.EqualValues(t, 7, accepted.ID)
	require.NotNil(t, accepted.RunningUntil)
	require.Equal(t, 600, accepted.TimeoutSeconds)
	runCtx := <-entered
	cancelRequest()
	require.NoError(t, runCtx.Err(), "disconnecting the accepted request must not cancel the upstream")
	// The response owns its time pointers; callers cannot mutate the live lease.
	originalLease := *accepted.RunningUntil
	*accepted.RunningUntil = time.Time{}
	accepted.ModelID = "client-modified"
	plans.mu.Lock()
	require.Equal(t, originalLease, *plans.plan.RunningUntil)
	plans.mu.Unlock()
	close(finish)
	awaitDegradationWorkers(t, runner)
	require.Equal(t, "success", results.saved.Status)
	require.Equal(t, "<svg/>", results.saved.ResponseText)
	require.Equal(t, "chosen-model", results.saved.ModelID)
	require.False(t, plans.scheduled)
	require.True(t, plans.completed)
	require.Nil(t, plans.nextRun, "manual execution must not postpone the periodic run")
	require.Nil(t, plans.plan.RunningUntil)
	require.Zero(t, len(runner.workers))
}

func TestDegradationTestAsyncConcurrentRequestsAndSchedulerShareLease(t *testing.T) {
	entered := make(chan struct{}, 1)
	finish := make(chan struct{})
	var calls atomic.Int32
	tester := degradationTesterFunc(func(ctx context.Context, _ int64, _ string, _ string) (*DegradationTestResult, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-finish:
			return &DegradationTestResult{Status: "success"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	runner, plans, results := newDegradationRunnerTest(t, tester)
	// A second process/service instance shares the repository's atomic lease.
	other := NewDegradationTestRunnerService(plans, NewDegradationTestService(plans, results), nil, nil)
	other.accountTestSvc = tester
	t.Cleanup(other.Stop)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var requests sync.WaitGroup
	for _, instance := range []*DegradationTestRunnerService{runner, other} {
		requests.Add(1)
		go func(instance *DegradationTestRunnerService) {
			defer requests.Done()
			<-start
			_, err := instance.RunNow(context.Background(), 7)
			errs <- err
		}(instance)
	}
	close(start)
	requests.Wait()
	close(errs)
	accepted, busy := 0, 0
	for err := range errs {
		if err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, ErrDegradationTestRunning)
			busy++
		}
	}
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, busy)
	<-entered
	_, err := other.runOnePlan(context.Background(), 7, true)
	require.ErrorIs(t, err, ErrDegradationTestRunning)
	require.EqualValues(t, 1, calls.Load())
	close(finish)
	awaitDegradationWorkers(t, runner)
	awaitDegradationWorkers(t, other)
	require.True(t, plans.completed)
}

func TestDegradationTestAsyncTimeoutMatchesClaimedLease(t *testing.T) {
	for _, seconds := range []int{60, 600, 1800} {
		entered := make(chan context.Context, 1)
		finish := make(chan struct{})
		runner, plans, _ := newDegradationRunnerTest(t, func(ctx context.Context, _ int64, _ string, _ string) (*DegradationTestResult, error) {
			entered <- ctx
			<-finish
			return &DegradationTestResult{Status: "success"}, nil
		})
		plans.plan.TimeoutSeconds = seconds
		plan, err := runner.RunNow(context.Background(), 7)
		require.NoError(t, err)
		runCtx := <-entered
		deadline, ok := runCtx.Deadline()
		require.True(t, ok)
		require.Equal(t, time.Minute, plan.RunningUntil.Sub(deadline))
		require.InDelta(t, seconds, time.Until(deadline).Seconds(), 1)
		close(finish)
		awaitDegradationWorkers(t, runner)
	}
}

func TestDegradationTestTimeoutPersistsFailureAndReleasesLease(t *testing.T) {
	runner, plans, results := newDegradationRunnerTest(t, func(ctx context.Context, _ int64, _ string, _ string) (*DegradationTestResult, error) {
		<-ctx.Done()
		return &DegradationTestResult{Status: "success", ResponseText: "partial"}, ctx.Err()
	})
	plan := cloneDegradationTestPlan(&plans.plan)
	plan.TimeoutSeconds = 60
	// An already elapsed deadline verifies timeout handling without a minute wait.
	result, err := runner.executeClaimedPlan(plan, false, time.Now().Add(-61*time.Second))
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.ErrorMessage, "timed out after 60 seconds")
	require.Equal(t, "partial", results.saved.ResponseText)
	require.True(t, plans.completed)
	require.Nil(t, plans.plan.RunningUntil)
}

func TestDegradationTestAsyncStopCancelsAndPersistsBeforeReturning(t *testing.T) {
	entered := make(chan struct{})
	runner, plans, results := newDegradationRunnerTest(t, func(ctx context.Context, _ int64, _ string, _ string) (*DegradationTestResult, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	_, err := runner.RunNow(context.Background(), 7)
	require.NoError(t, err)
	<-entered
	runner.Stop()
	require.True(t, plans.completed)
	require.Nil(t, plans.plan.RunningUntil)
	require.Equal(t, "failed", results.saved.Status)
	require.Contains(t, results.saved.ErrorMessage, context.Canceled.Error())
	require.Zero(t, len(runner.workers))
	_, err = runner.RunNow(context.Background(), 7)
	require.ErrorIs(t, err, ErrDegradationTestStopped)
}

func TestDegradationTestAsyncPersistenceFailureStillReleasesWorkerAndLease(t *testing.T) {
	runner, plans, results := newDegradationRunnerTest(t, func(context.Context, int64, string, string) (*DegradationTestResult, error) {
		return &DegradationTestResult{Status: "success"}, nil
	})
	results.err = errors.New("database unavailable")
	_, err := runner.RunNow(context.Background(), 7)
	require.NoError(t, err, "acceptance is independent of the eventual persistence outcome")
	awaitDegradationWorkers(t, runner)
	require.True(t, plans.completed)
	require.Zero(t, len(runner.workers))
}
