//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDownstreamAssessmentMembershipAndUsage(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	group := mustCreateGroup(t, client, &service.Group{Name: "assessment-target", IsExclusive: true})
	other := mustCreateGroup(t, client, &service.Group{Name: "assessment-other"})
	account := mustCreateAccount(t, client, &service.Account{Name: "assessment-account"})
	newUser := func(name string, allowed bool) *service.User {
		u := &service.User{Email: name + "@assessment.test"}
		if allowed {
			u.AllowedGroups = []int64{group.ID}
		}
		return mustCreateUser(t, client, u)
	}
	idle := newUser("idle", true)
	below := newUser("below", true)
	exact := newUser("exact", true)
	keyOnly := newUser("key-only", false)
	deletedKey := newUser("deleted-key", false)
	deletedUser := newUser("deleted-user", true)
	otherUser := newUser("other-group", false)
	subscriber := newUser("subscriber", false)
	expired := newUser("expired", false)
	future := newUser("future", false)
	newKey := func(u *service.User, groupID int64, suffix string) *service.APIKey {
		return mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, GroupID: &groupID, Key: fmt.Sprintf("assessment-%d-%s", u.ID, suffix), Name: suffix})
	}
	exactKey := newKey(exact, group.ID, "one")
	newKey(exact, group.ID, "two")
	belowKey := newKey(below, group.ID, "one")
	keyOnlyKey := newKey(keyOnly, group.ID, "one")
	deletedKeyKey := newKey(deletedKey, group.ID, "one")
	otherKey := newKey(otherUser, other.ID, "one")
	subscriberKey := newKey(subscriber, other.ID, "one")
	now := time.Now()
	for _, s := range []struct {
		u          *service.User
		start, end time.Time
	}{
		{subscriber, now.Add(-time.Hour), now.Add(time.Hour)},
		{expired, now.Add(-2 * time.Hour), now.Add(-time.Hour)},
		{future, now.Add(time.Hour), now.Add(2 * time.Hour)},
	} {
		_, err := client.UserSubscription.Create().SetUserID(s.u.ID).SetGroupID(group.ID).SetStartsAt(s.start).SetExpiresAt(s.end).SetStatus("active").Save(ctx)
		require.NoError(t, err)
	}
	require.NoError(t, client.APIKey.DeleteOneID(deletedKeyKey.ID).Exec(ctx))
	require.NoError(t, client.User.DeleteOneID(deletedUser.ID).Exec(ctx))
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	sequence := 0
	addUsage := func(u *service.User, key *service.APIKey, groupID int64, at time.Time, input, output, creation, read int) {
		sequence++
		_, err := repo.Create(ctx, &service.UsageLog{UserID: u.ID, APIKeyID: key.ID, AccountID: account.ID,
			RequestID: fmt.Sprintf("assessment-%d-%d", u.ID, sequence),
			GroupID:   &groupID, Model: "assessment-model", CreatedAt: at, InputTokens: input, OutputTokens: output,
			CacheCreationTokens: creation, CacheReadTokens: read, ActualCost: 1, TotalCost: 1})
		require.NoError(t, err)
	}
	// Membership is selected by group, but usage is across all groups. This user
	// reaches exactly 7B using the selected group, another group and a group-less log.
	// Summing four INTEGER columns must not overflow before SUM.
	addUsage(exact, exactKey, group.ID, start, 1000000000, 1000000000, 1000000000, 1000000000)
	addUsage(exact, exactKey, other.ID, start, 500000000, 500000000, 500000000, 500000000)
	_, err := repo.Create(ctx, &service.UsageLog{UserID: exact.ID, APIKeyID: exactKey.ID, AccountID: account.ID,
		RequestID: fmt.Sprintf("assessment-no-group-%d", exact.ID), Model: "assessment-model", CreatedAt: start,
		InputTokens: 250000000, OutputTokens: 250000000, CacheCreationTokens: 250000000, CacheReadTokens: 250000000,
		ActualCost: 1, TotalCost: 1})
	require.NoError(t, err)
	addUsage(below, belowKey, group.ID, end.Add(-time.Second), 1500000000, 1499999999, 2000000000, 2000000000)
	addUsage(keyOnly, keyOnlyKey, group.ID, start, 2000000000, 2000000000, 2000000000, 2000000000)
	// Out-of-range dates and usage by a non-member must still be excluded.
	addUsage(exact, exactKey, group.ID, start.Add(-time.Second), 100, 100, 100, 100)
	addUsage(exact, exactKey, group.ID, end, 100, 100, 100, 100)
	latest := end.AddDate(0, 0, 2)
	addUsage(exact, exactKey, other.ID, latest, 100, 100, 100, 100)
	addUsage(subscriber, subscriberKey, other.ID, start.Add(-time.Hour), 100, 100, 100, 100)
	addUsage(otherUser, otherKey, group.ID, start, 100, 100, 100, 100)
	filter := service.DownstreamAssessmentFilter{GroupID: group.ID, StartTime: start, EndTime: end, Days: 7, DailyThreshold: 1000000000}
	var ids []int64
	var qualified []bool
	for page := 1; page <= 3; page++ {
		result, err := repo.GetDownstreamAssessment(ctx, pagination.PaginationParams{Page: page, PageSize: 2}, filter)
		require.NoError(t, err)
		require.Equal(t, int64(5), result.Total)
		require.Equal(t, int64(3), result.UnqualifiedCount)
		require.Equal(t, int64(2), result.QualifiedCount)
		require.Equal(t, int64(21999999999), result.TotalTokens)
		for _, row := range result.Items {
			ids = append(ids, row.UserID)
			qualified = append(qualified, row.Qualified)
			if row.UserID == exact.ID {
				require.Equal(t, int64(7000000000), row.TotalTokens)
				require.Equal(t, int64(3), row.TotalRequests)
				require.Equal(t, float64(1000000000), row.DailyAverage)
				require.NotNil(t, row.LastUsedAt)
				require.Equal(t, latest, *row.LastUsedAt)
			}
			if row.UserID == idle.ID {
				require.Nil(t, row.LastUsedAt)
			}
			if row.UserID == subscriber.ID {
				require.Zero(t, row.TotalTokens)
				require.NotNil(t, row.LastUsedAt)
				require.Equal(t, start.Add(-time.Hour), *row.LastUsedAt)
			}
		}
	}
	require.Equal(t, []int64{idle.ID, subscriber.ID, below.ID, exact.ID, keyOnly.ID}, ids)
	require.Equal(t, []bool{false, false, false, true, true}, qualified)
	result, err := repo.GetDownstreamAssessment(ctx, pagination.PaginationParams{Page: 20, PageSize: 2}, filter)
	require.NoError(t, err)
	require.Equal(t, int64(5), result.Total)
	require.Empty(t, result.Items)
}
