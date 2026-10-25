package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDegradationTestIntervalsAndLegacyCron(t *testing.T) {
	from := time.Date(2026, 10, 24, 13, 17, 23, 123, time.UTC)
	for _, tc := range []struct {
		expression string
		want       time.Time
	}{
		{"@every 1m", from.Add(time.Minute)},
		{"@every 90m", from.Add(90 * time.Minute)},
		{"@every 720h", from.Add(30 * 24 * time.Hour)},
		{"*/30 * * * *", time.Date(2026, 10, 24, 13, 30, 0, 0, time.UTC)},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			next, err := computeDegradationNextRun(tc.expression, from)
			require.NoError(t, err)
			require.Equal(t, tc.want, next)
		})
	}
	for _, expression := range []string{"@every 59s", "@every -1m", "@every 721h", "@every nope", "", "0 0 31 2 *"} {
		_, err := computeDegradationNextRun(expression, from)
		require.Error(t, err, expression)
	}
}

func TestDegradationTestPlanValidation(t *testing.T) {
	valid := DegradationTestPlan{AccountID: 1, ModelID: "model", Prompt: DefaultDegradationTestPrompt, CronExpression: "@every 90m", MaxResults: 24}
	for _, tc := range []struct {
		name   string
		mutate func(*DegradationTestPlan)
	}{
		{"negative account", func(p *DegradationTestPlan) { p.AccountID = -1 }},
		{"long model", func(p *DegradationTestPlan) { p.ModelID = strings.Repeat("a", 101) }},
		{"missing generation model", func(p *DegradationTestPlan) { p.ModelID = "" }},
		{"whitespace generation model", func(p *DegradationTestPlan) { p.ModelID = "  " }},
		{"long prompt", func(p *DegradationTestPlan) { p.Prompt = strings.Repeat("鹈", DegradationTestMaxPromptLength+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := valid
			tc.mutate(&plan)
			require.Error(t, validateDegradationTestPlan(&plan))
		})
	}
	valid.Prompt = ""
	require.Error(t, validateDegradationTestPlan(&valid))
	valid.ModelID = ""
	valid.Prompt = DefaultDegradationTestPrompt
	require.Error(t, validateDegradationTestPlan(&valid))
	valid.ModelID = "chosen-model"
	valid.Prompt = strings.Repeat("鹈", DegradationTestMaxPromptLength)
	require.NoError(t, validateDegradationTestPlan(&valid))
}

type degradationPlanRepoStub struct {
	DegradationTestPlanRepository
	mu        sync.Mutex
	plan      DegradationTestPlan
	claim     bool
	scheduled bool
	completed bool
	nextRun   *time.Time
}

func (r *degradationPlanRepoStub) Create(_ context.Context, plan *DegradationTestPlan) (*DegradationTestPlan, error) {
	r.plan = *plan
	return plan, nil
}

func (r *degradationPlanRepoStub) Update(_ context.Context, plan *DegradationTestPlan) (*DegradationTestPlan, error) {
	r.plan = *plan
	return plan, nil
}

func (r *degradationPlanRepoStub) GetByID(context.Context, int64) (*DegradationTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	plan := r.plan
	return &plan, nil
}

func (r *degradationPlanRepoStub) ClaimRun(ctx context.Context, _ int64, scheduled bool, now time.Time) (*DegradationTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.scheduled = scheduled
	if !r.claim || (r.plan.RunningUntil != nil && r.plan.RunningUntil.After(now)) {
		return nil, nil
	}
	timeout := r.plan.TimeoutSeconds
	if timeout == 0 {
		timeout = DegradationTestDefaultTimeoutSeconds
	}
	until := now.Add(time.Duration(timeout)*time.Second + degradationTestLeaseGrace)
	r.plan.RunningUntil = &until
	plan := r.plan
	plan.TimeoutSeconds = timeout
	return cloneDegradationTestPlan(&plan), nil
}

func (r *degradationPlanRepoStub) CompleteRun(ctx context.Context, _ *DegradationTestPlan, _ time.Time, next *time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed = true
	r.nextRun = next
	r.plan.RunningUntil = nil
	return nil
}

type degradationResultRepoStub struct {
	DegradationTestResultRepository
	saved     *DegradationTestResult
	err       error
	keepCount int
	listLimit int
}

func (r *degradationResultRepoStub) Create(ctx context.Context, result *DegradationTestResult) (*DegradationTestResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	saved := *result
	saved.ID = 12
	saved.CreatedAt = time.Now()
	r.saved = &saved
	return &saved, nil
}

func (r *degradationResultRepoStub) PruneOldResults(ctx context.Context, _ int64, keepCount int) error {
	r.keepCount = keepCount
	return ctx.Err()
}

func (r *degradationResultRepoStub) ListByPlanID(_ context.Context, _ int64, limit int) ([]*DegradationTestResult, error) {
	r.listLimit = limit
	return []*DegradationTestResult{}, nil
}

func TestDegradationTestRetentionIsFixedForLegacyPlansAndClients(t *testing.T) {
	for _, requested := range []int{-1, 0, 1, 10, 24, 50, 1000} {
		plans := &degradationPlanRepoStub{}
		results := &degradationResultRepoStub{}
		svc := NewDegradationTestService(plans, results)
		plan := &DegradationTestPlan{AccountID: 1, ModelID: "model", Prompt: DefaultDegradationTestPrompt, CronExpression: "@every 30m", MaxResults: requested}
		created, err := svc.CreatePlan(context.Background(), plan)
		require.NoError(t, err)
		require.Equal(t, 24, created.MaxResults)
		require.Equal(t, 24, plans.plan.MaxResults)
		plan.MaxResults = requested
		updated, err := svc.UpdatePlan(context.Background(), plan)
		require.NoError(t, err)
		require.Equal(t, 24, updated.MaxResults)
		require.Equal(t, 24, plans.plan.MaxResults)
		require.NoError(t, svc.SaveResult(context.Background(), 7, requested, &DegradationTestResult{Status: "success"}))
		require.Equal(t, 24, results.keepCount)
	}
}

func TestDegradationTestResultLimitDefaultsToTenAndClampsToTwentyFour(t *testing.T) {
	results := &degradationResultRepoStub{}
	svc := NewDegradationTestService(nil, results)
	for _, tc := range []struct{ requested, want int }{{0, 10}, {-1, 10}, {1, 1}, {7, 7}, {10, 10}, {11, 11}, {24, 24}, {50, 24}, {1000, 24}} {
		_, err := svc.ListResults(context.Background(), 7, tc.requested)
		require.NoError(t, err)
		require.Equal(t, tc.want, results.listLimit)
	}
}

type degradationTesterFunc func(context.Context, int64, string, string) (*DegradationTestResult, error)

func (f degradationTesterFunc) RunGenerationBackground(ctx context.Context, id int64, model string, prompt string, _ ...string) (*DegradationTestResult, error) {
	return f(ctx, id, model, prompt)
}

func newDegradationRunnerTest(t *testing.T, tester degradationTesterFunc) (*DegradationTestRunnerService, *degradationPlanRepoStub, *degradationResultRepoStub) {
	t.Helper()
	plans := &degradationPlanRepoStub{claim: true, plan: DegradationTestPlan{
		ID: 7, AccountID: 9, ModelID: "chosen-model", Prompt: DefaultDegradationTestPrompt,
		CronExpression: "@every 90m", MaxResults: 50, Enabled: false,
	}}
	results := &degradationResultRepoStub{}
	svc := NewDegradationTestService(plans, results)
	runner := NewDegradationTestRunnerService(plans, svc, nil, nil)
	runner.accountTestSvc = tester
	t.Cleanup(runner.Stop)
	return runner, plans, results
}

func TestDegradationTestManualRunPersistsSnapshotAndPreservesSchedule(t *testing.T) {
	runner, plans, results := newDegradationRunnerTest(t, func(_ context.Context, id int64, model string, prompts string) (*DegradationTestResult, error) {
		require.EqualValues(t, 9, id)
		require.Equal(t, "chosen-model", model)
		require.Equal(t, DefaultDegradationTestPrompt, prompts)
		return &DegradationTestResult{Status: "success", ResponseText: "<html><svg/></html>"}, nil
	})
	result, err := runner.runOnePlan(context.Background(), 7, false)
	require.NoError(t, err)
	require.EqualValues(t, 12, result.ID)
	require.Equal(t, "chosen-model", results.saved.ModelID)
	require.Equal(t, DefaultDegradationTestPrompt, results.saved.Prompt)
	require.False(t, plans.scheduled)
	require.True(t, plans.completed)
	require.Nil(t, plans.nextRun)
	require.Equal(t, 24, results.keepCount)
}

func TestDegradationTestFailuresAreSavedAndScheduleAdvances(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     degradationTesterFunc
		message string
	}{
		{"error", func(context.Context, int64, string, string) (*DegradationTestResult, error) {
			return nil, errors.New("upstream refused")
		}, "upstream refused"},
		{"timeout", func(context.Context, int64, string, string) (*DegradationTestResult, error) {
			return nil, context.DeadlineExceeded
		}, "context deadline exceeded"},
		{"panic", func(context.Context, int64, string, string) (*DegradationTestResult, error) {
			panic("broken provider")
		}, "broken provider"},
		{"empty", func(context.Context, int64, string, string) (*DegradationTestResult, error) { return nil, nil }, "no result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, plans, results := newDegradationRunnerTest(t, tc.run)
			result, err := runner.runOnePlan(context.Background(), 7, true)
			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.Contains(t, results.saved.ErrorMessage, tc.message)
			require.True(t, plans.completed)
			require.NotNil(t, plans.nextRun)
			require.Equal(t, result.FinishedAt.Add(90*time.Minute), *plans.nextRun)
		})
	}
}

func TestDegradationTestCancellationStillPersistsFailure(t *testing.T) {
	var runner *DegradationTestRunnerService
	runner, plans, results := newDegradationRunnerTest(t, func(ctx context.Context, _ int64, _ string, _ string) (*DegradationTestResult, error) {
		runner.cancelBackground()
		return &DegradationTestResult{Status: "success", ResponseText: "partial"}, ctx.Err()
	})
	result, err := runner.runOnePlan(context.Background(), 7, false)
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "context canceled", results.saved.ErrorMessage)
	require.Equal(t, "partial", results.saved.ResponseText)
	require.True(t, plans.completed)
}

func TestDegradationTestBusyAndCapacityDoNotCallProvider(t *testing.T) {
	runner, plans, results := newDegradationRunnerTest(t, func(context.Context, int64, string, string) (*DegradationTestResult, error) {
		t.Fatal("provider must not be called")
		return nil, nil
	})
	plans.claim = false
	_, err := runner.RunNow(context.Background(), 7)
	require.ErrorIs(t, err, ErrDegradationTestRunning)
	require.Nil(t, results.saved)
	require.Zero(t, len(runner.workers))
	for range cap(runner.workers) {
		runner.workers <- struct{}{}
	}
	_, err = runner.RunNow(context.Background(), 7)
	require.ErrorIs(t, err, ErrDegradationTestCapacity)
}

func TestDegradationTestPersistenceErrorReleasesLease(t *testing.T) {
	runner, plans, results := newDegradationRunnerTest(t, func(context.Context, int64, string, string) (*DegradationTestResult, error) {
		return &DegradationTestResult{Status: "success"}, nil
	})
	results.err = errors.New("storage unavailable")
	_, err := runner.runOnePlan(context.Background(), 7, false)
	require.ErrorContains(t, err, "storage unavailable")
	require.True(t, plans.completed)
	require.Zero(t, len(runner.workers))
}
