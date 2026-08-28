//go:build integration

package repository

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type integrationAPIKeyCooldownSettingsProvider struct{}

func (integrationAPIKeyCooldownSettingsProvider) GetAPIKeyFailureCooldownSettings(context.Context) (*service.APIKeyFailureCooldownSettings, error) {
	return service.DefaultAPIKeyFailureCooldownSettings(), nil
}

func TestAPIKeyAccountCooldownAppliesAcrossGroupsAndSurvivesRepositoryRebuild(t *testing.T) {
	if integrationDB == nil || integrationEntClient == nil {
		t.Skip("integration database is unavailable")
	}
	groupA := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "cooldown-group-a", Platform: service.PlatformOpenAI})
	groupB := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "cooldown-group-b", Platform: service.PlatformOpenAI})
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        "cooldown-cross-group-account",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
	})
	mustBindAccountToGroup(t, integrationEntClient, account.ID, groupA.ID, 1)
	mustBindAccountToGroup(t, integrationEntClient, account.ID, groupB.ID, 1)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id IN ($1, $2)", groupA.ID, groupB.ID)
	})
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)

	for _, groupID := range []int64{groupA.ID, groupB.ID} {
		accounts, err := repo.ListSchedulableByGroupIDAndPlatform(context.Background(), groupID, service.PlatformOpenAI)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
	}

	until := time.Now().UTC().Add(time.Second)
	require.NoError(t, repo.SetTempUnschedulable(context.Background(), account.ID, until, `{"source":"api_key_failure_cooldown"}`))

	rebuiltRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	for _, groupID := range []int64{groupA.ID, groupB.ID} {
		accounts, err := rebuiltRepo.ListSchedulableByGroupIDAndPlatform(context.Background(), groupID, service.PlatformOpenAI)
		require.NoError(t, err)
		require.Empty(t, accounts, "a persisted account cooldown must exclude every group after repository rebuild")
	}

	require.Eventually(t, func() bool {
		for _, groupID := range []int64{groupA.ID, groupB.ID} {
			accounts, err := rebuiltRepo.ListSchedulableByGroupIDAndPlatform(context.Background(), groupID, service.PlatformOpenAI)
			if err != nil || len(accounts) != 1 || accounts[0].ID != account.ID {
				return false
			}
		}
		return true
	}, 3*time.Second, 25*time.Millisecond, "expired account cooldown must recover without a background clear operation")
}

func (s *AccountRepoSuite) TestAPIKeyModelCooldownIsIsolatedAndExpiresAfterRepositoryRebuild() {
	account := mustCreateAccount(s.T(), s.client, &service.Account{
		Name:        "cooldown-model-isolation-account",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
	})
	until := time.Now().UTC().Add(2 * time.Second)
	s.Require().NoError(s.repo.SetModelRateLimit(s.ctx, account.ID, "model-m1", until, `{"family":"model_unsupported"}`))

	rebuiltRepo := newAccountRepositoryWithSQL(s.client, s.repo.sql, nil)
	loaded, err := rebuiltRepo.GetByID(s.ctx, account.ID)
	s.Require().NoError(err)
	s.Require().False(loaded.IsSchedulableForModel("model-m1"))
	s.Require().True(loaded.IsSchedulableForModel("model-m2"), "a model-scoped cooldown must not exclude another model")

	s.Require().Eventually(func() bool {
		fresh, loadErr := rebuiltRepo.GetByID(s.ctx, account.ID)
		return loadErr == nil && fresh.IsSchedulableForModel("model-m1") && fresh.IsSchedulableForModel("model-m2")
	}, 5*time.Second, 25*time.Millisecond, "expired model cooldown must recover without a background clear operation")
}

func TestAPIKeyCooldownGuardBlocksAStaleSchedulerSnapshotAcrossInstances(t *testing.T) {
	if integrationDB == nil || integrationEntClient == nil || integrationRedis == nil {
		t.Skip("integration dependencies are unavailable")
	}
	account := mustCreateAccount(t, integrationEntClient, &service.Account{
		Name:        "cooldown-stale-snapshot-account",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", account.ID)
	})

	repoA := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	staleSnapshot, err := repoA.GetByID(context.Background(), account.ID)
	require.NoError(t, err)
	require.True(t, staleSnapshot.IsSchedulable())

	redisClient := testRedis(t)
	coordinatorA := service.NewAPIKeyCooldownCoordinator(
		repoA,
		NewAPIKeyCooldownStore(redisClient),
		integrationAPIKeyCooldownSettingsProvider{},
	)
	now := time.Now().UTC()
	decision, err := coordinatorA.ObserveFailure(context.Background(), account, service.APIKeyFailureObservation{
		AttemptID:      "stale-snapshot-failure",
		AttemptStarted: now,
		AccountID:      account.ID,
		AccountType:    account.Type,
		Platform:       account.Platform,
		Model:          "gpt-5",
		HTTPStatus:     http.StatusBadGateway,
		RequestSent:    true,
		ReplaySafe:     true,
	}, now)
	require.NoError(t, err)
	require.True(t, decision.ShouldCooldown())
	require.True(t, staleSnapshot.IsSchedulable(), "the test must retain a scheduler snapshot captured before persistence")

	repoB := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	coordinatorB := service.NewAPIKeyCooldownCoordinator(
		repoB,
		NewAPIKeyCooldownStore(redisClient),
		integrationAPIKeyCooldownSettingsProvider{},
	)
	blocked, token, err := coordinatorB.Check(context.Background(), staleSnapshot, "gpt-5", now.Add(time.Second))
	require.NoError(t, err)
	require.True(t, blocked, "the shared pre-send guard must close the scheduler snapshot propagation race")
	require.Equal(t, decision.Generation, token.Generations[service.APIKeyCooldownKey{
		AccountID: account.ID,
		Family:    service.APIKeyFailureTransientUpstream,
		Scope:     service.APIKeyCooldownScopeAccount,
	}.RedisKey()])

	fresh, err := repoB.GetByID(context.Background(), account.ID)
	require.NoError(t, err)
	require.False(t, fresh.IsSchedulable(), "the database cooldown must remain the restart fallback")
}
