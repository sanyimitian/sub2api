//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// SQL mocks cannot detect PostgreSQL's conflicting varchar/text inference when
// a schedule parameter is used in both an assignment and a CASE comparison.
func TestDegradationTestPlanUpdatePostgres(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name: "degradation-update-regression", Platform: "openai", Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "unused-test-key"},
	})
	t.Cleanup(func() { _ = integrationEntClient.Account.DeleteOneID(account.ID).Exec(ctx) })
	repo := NewDegradationTestPlanRepository(integrationDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	originalNext := now.Add(time.Hour)
	plan, err := repo.Create(ctx, &service.DegradationTestPlan{
		AccountID: account.ID, ModelID: "test-model", Prompt: "original prompt",
		ReasoningEffort: "high", CronExpression: "@every 1h", Enabled: true, NextRunAt: &originalNext,
	})
	require.NoError(t, err)
	planID := plan.ID
	t.Cleanup(func() { _ = repo.Delete(ctx, planID) })

	// Saving fields must succeed without postponing the scheduled run.
	proposedNext := now.Add(2 * time.Hour)
	plan.ModelID, plan.Prompt, plan.ReasoningEffort = "edited-model", "edited prompt", "xhigh"
	plan.NextRunAt = &proposedNext
	plan, err = repo.Update(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, "edited-model", plan.ModelID)
	require.Equal(t, "edited prompt", plan.Prompt)
	require.Equal(t, "xhigh", plan.ReasoningEffort)
	require.True(t, plan.NextRunAt.Equal(originalNext))

	// Disabling preserves the schedule; re-enabling starts a fresh interval.
	plan.Enabled, plan.NextRunAt = false, &proposedNext
	plan, err = repo.Update(ctx, plan)
	require.NoError(t, err)
	require.False(t, plan.Enabled)
	require.True(t, plan.NextRunAt.Equal(originalNext))
	plan.Enabled, plan.NextRunAt = true, &proposedNext
	plan, err = repo.Update(ctx, plan)
	require.NoError(t, err)
	require.True(t, plan.Enabled)
	require.True(t, plan.NextRunAt.Equal(proposedNext))

	// Changing the interval reschedules and allows clearing the effort.
	changedNext := now.Add(3 * time.Hour)
	plan.CronExpression, plan.ReasoningEffort, plan.NextRunAt = "@every 3h", "", &changedNext
	plan, err = repo.Update(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, "@every 3h", plan.CronExpression)
	require.Empty(t, plan.ReasoningEffort)
	require.True(t, plan.NextRunAt.Equal(changedNext))
}
