//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDownstreamAutoRatesAcrossGroupsAndManualPriority(t *testing.T) {
	ctx := context.Background()
	client := integrationEntClient
	group := mustCreateGroup(t, client, &service.Group{Name: "auto-rate-target-" + time.Now().Format("150405.000000000"), IsExclusive: true})
	other := mustCreateGroup(t, client, &service.Group{Name: "auto-rate-other-" + time.Now().Format("150405.000000000")})
	user := mustCreateUser(t, client, &service.User{Email: "auto-rate-" + time.Now().Format("150405.000000000") + "@example.test", AllowedGroups: []int64{group.ID}})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `UPDATE downstream_auto_rate_config SET enabled = FALSE, mode = 'weekly', group_ids = '{}', last_run_date = NULL WHERE id = TRUE`)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM downstream_auto_user_group_rates WHERE user_id = $1`, user.ID)
		_ = client.User.DeleteOneID(user.ID).Exec(context.Background())
		_ = client.Group.DeleteOneID(group.ID).Exec(context.Background())
		_ = client.Group.DeleteOneID(other.ID).Exec(context.Background())
	})
	account := mustCreateAccount(t, client, &service.Account{Name: "auto-rate-account-" + time.Now().Format("150405.000000000")})
	defer client.Account.DeleteOneID(account.ID).Exec(ctx)
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: "auto-rate-key-" + time.Now().Format("150405.000000000"), Name: "auto-rate"})
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	friday, err := time.ParseInLocation("2006-01-02", "2026-10-16", loc)
	require.NoError(t, err)
	workingDays := []time.Time{friday.AddDate(0, 0, -6), friday.AddDate(0, 0, -4), friday.AddDate(0, 0, -3), friday.AddDate(0, 0, -2), friday.AddDate(0, 0, -1), friday}
	for _, day := range workingDays[1:] {
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs (user_id, api_key_id, account_id, model, group_id, input_tokens, created_at) VALUES ($1, $2, $3, 'auto-test', $4, 600000000, $5)`, user.ID, key.ID, account.ID, other.ID, day.Add(time.Hour))
		require.NoError(t, err)
	}
	// The previous Saturday's makeup usage enters this Friday's six-day average.
	for i := 0; i < 2; i++ {
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs (user_id, api_key_id, account_id, model, group_id, input_tokens, created_at) VALUES ($1, $2, $3, 'auto-test', $4, 2000000000, $5)`, user.ID, key.ID, account.ID, other.ID, workingDays[0].Add(time.Hour))
		require.NoError(t, err)
	}
	// The ordinary Sunday must not affect the selected tier.
	for _, day := range []time.Time{friday.AddDate(0, 0, -5)} {
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs (user_id, api_key_id, account_id, model, group_id, input_tokens, created_at) VALUES ($1, $2, $3, 'auto-test', $4, 2000000000, $5)`, user.ID, key.ID, account.ID, other.ID, day.Add(time.Hour))
		require.NoError(t, err)
	}
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	config := service.DownstreamAutoRateConfig{Enabled: true, Mode: service.DownstreamAutoRateWeekly, GroupIDs: []int64{group.ID}, Tiers: []service.DownstreamAutoRateTier{{MinTokens: 500000000, RateMultiplier: 0.17}, {MinTokens: 1000000000, RateMultiplier: 0.155}}}
	require.NoError(t, repo.SaveDownstreamAutoRateConfig(ctx, config))
	applied, err := repo.ApplyDownstreamAutoRates(ctx, friday, workingDays, config)
	require.NoError(t, err)
	require.True(t, applied)
	rateRepo := NewUserGroupRateRepository(integrationDB)
	rate, err := rateRepo.GetByUserAndGroup(ctx, user.ID, group.ID)
	require.NoError(t, err)
	require.InDelta(t, 0.155, *rate, 1e-10)
	effective, err := rateRepo.(*userGroupRateRepository).GetEffectiveByUserID(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, 0.155, effective[group.ID], 1e-10)
	manual := 0.2
	require.NoError(t, rateRepo.SyncUserGroupRates(ctx, user.ID, map[int64]*float64{group.ID: &manual}))
	rate, err = rateRepo.GetByUserAndGroup(ctx, user.ID, group.ID)
	require.NoError(t, err)
	require.InDelta(t, manual, *rate, 1e-10)
	effective, err = rateRepo.(*userGroupRateRepository).GetEffectiveByUserID(ctx, user.ID)
	require.NoError(t, err)
	require.InDelta(t, manual, effective[group.ID], 1e-10)
	applied, err = repo.ApplyDownstreamAutoRates(ctx, friday, workingDays, config)
	require.NoError(t, err)
	require.False(t, applied)
	manualRates := map[int64]*float64{group.ID: nil}
	require.NoError(t, rateRepo.SyncUserGroupRates(ctx, user.ID, manualRates))
	nextFriday := friday.AddDate(0, 0, 7)
	applied, err = repo.ApplyDownstreamAutoRates(ctx, nextFriday, []time.Time{nextFriday.AddDate(0, 0, -4), nextFriday}, config)
	require.NoError(t, err)
	require.True(t, applied)
	rate, err = rateRepo.GetByUserAndGroup(ctx, user.ID, group.ID)
	require.NoError(t, err)
	require.Nil(t, rate)
	// Daily mode uses only the target workday, excluding earlier high usage.
	config.Mode = service.DownstreamAutoRateDaily
	require.NoError(t, repo.SaveDownstreamAutoRateConfig(ctx, config))
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs (user_id, api_key_id, account_id, model, group_id, input_tokens, created_at) VALUES ($1, $2, $3, 'auto-test', $4, 2000000000, $5)`, user.ID, key.ID, account.ID, other.ID, nextFriday.AddDate(0, 0, -1).Add(time.Hour))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO usage_logs (user_id, api_key_id, account_id, model, group_id, input_tokens, created_at) VALUES ($1, $2, $3, 'auto-test', $4, 600000000, $5)`, user.ID, key.ID, account.ID, other.ID, nextFriday.Add(time.Hour))
	require.NoError(t, err)
	applied, err = repo.ApplyDownstreamAutoRates(ctx, nextFriday, []time.Time{nextFriday}, config)
	require.NoError(t, err)
	require.True(t, applied)
	rate, err = rateRepo.GetByUserAndGroup(ctx, user.ID, group.ID)
	require.NoError(t, err)
	require.InDelta(t, 0.17, *rate, 1e-10)
	loaded, err := repo.GetDownstreamAutoRateConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, service.DownstreamAutoRateDaily, loaded.Mode)
	config.Enabled = false
	require.NoError(t, repo.SaveDownstreamAutoRateConfig(ctx, config))
	rate, err = rateRepo.GetByUserAndGroup(ctx, user.ID, group.ID)
	require.NoError(t, err)
	require.Nil(t, rate)
}
