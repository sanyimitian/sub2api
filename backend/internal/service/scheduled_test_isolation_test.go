package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type originalScheduledPlanRepo struct {
	ScheduledTestPlanRepository
}

func (r *originalScheduledPlanRepo) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return plan, nil
}

type originalScheduledResultRepo struct {
	ScheduledTestResultRepository
	limit int
	keep  int
}

func (r *originalScheduledResultRepo) ListByPlanID(_ context.Context, _ int64, limit int) ([]*ScheduledTestResult, error) {
	r.limit = limit
	return nil, nil
}

func (r *originalScheduledResultRepo) Create(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	return result, nil
}

func (r *originalScheduledResultRepo) PruneOldResults(_ context.Context, _ int64, keep int) error {
	r.keep = keep
	return nil
}

func TestScheduledTestKeepsOriginalRetentionRecoveryAndProbeParser(t *testing.T) {
	results := &originalScheduledResultRepo{}
	svc := NewScheduledTestService(&originalScheduledPlanRepo{}, results)
	for _, tc := range []struct{ requested, want int }{{0, 50}, {37, 37}, {100, 100}} {
		plan, err := svc.CreatePlan(context.Background(), &ScheduledTestPlan{AccountID: 1, CronExpression: "*/30 * * * *", MaxResults: tc.requested, AutoRecover: true})
		require.NoError(t, err)
		require.Equal(t, tc.want, plan.MaxResults)
		require.True(t, plan.AutoRecover)
		require.NoError(t, svc.SaveResult(context.Background(), 1, plan.MaxResults, &ScheduledTestResult{Status: "success"}))
		require.Equal(t, tc.want, results.keep)
		_, err = svc.ListResults(context.Background(), 1, tc.requested)
		require.NoError(t, err)
		require.Equal(t, tc.want, results.limit)
	}
	text, errMsg := parseTestSSEOutput("data: {\"type\":\"content\",\"text\":\"probe\"}\n\n")
	require.Equal(t, "probe", text)
	require.Empty(t, errMsg)
	_, errMsg = parseGenerationTestSSEOutput("data: {\"type\":\"content\",\"text\":\"partial\"}\n\n")
	require.Contains(t, errMsg, "without successful completion")
}
