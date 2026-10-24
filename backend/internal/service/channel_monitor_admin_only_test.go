//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type adminOnlyMonitorRepo struct {
	ChannelMonitorRepository
	monitors []*ChannelMonitor
	queried  [][]int64
	details  int
}

func (r *adminOnlyMonitorRepo) ListEnabled(context.Context) ([]*ChannelMonitor, error) {
	return r.monitors, nil
}

func (r *adminOnlyMonitorRepo) GetByID(_ context.Context, id int64) (*ChannelMonitor, error) {
	for _, monitor := range r.monitors {
		if monitor.ID == id {
			return monitor, nil
		}
	}
	return nil, ErrChannelMonitorNotFound
}

func (r *adminOnlyMonitorRepo) ListLatestForMonitorIDs(_ context.Context, ids []int64) (map[int64][]*ChannelMonitorLatest, error) {
	r.queried = append(r.queried, append([]int64{}, ids...))
	return map[int64][]*ChannelMonitorLatest{}, nil
}

func (r *adminOnlyMonitorRepo) ComputeAvailabilityForMonitors(_ context.Context, ids []int64, _ int) (map[int64][]*ChannelMonitorAvailability, error) {
	r.queried = append(r.queried, append([]int64{}, ids...))
	return map[int64][]*ChannelMonitorAvailability{}, nil
}

func (r *adminOnlyMonitorRepo) ListRecentHistoryForMonitors(_ context.Context, ids []int64, _ map[int64]string, _ int) (map[int64][]*ChannelMonitorHistoryEntry, error) {
	r.queried = append(r.queried, append([]int64{}, ids...))
	return map[int64][]*ChannelMonitorHistoryEntry{}, nil
}

func (r *adminOnlyMonitorRepo) ListLatestPerModel(context.Context, int64) ([]*ChannelMonitorLatest, error) {
	r.details++
	return []*ChannelMonitorLatest{}, nil
}

func (r *adminOnlyMonitorRepo) ComputeAvailability(context.Context, int64, int) ([]*ChannelMonitorAvailability, error) {
	r.details++
	return []*ChannelMonitorAvailability{}, nil
}

func TestChannelMonitorAdminOnlyFiltersBeforeAggregationWithoutMutatingSharedRows(t *testing.T) {
	repo := &adminOnlyMonitorRepo{monitors: []*ChannelMonitor{
		{ID: 1, Name: "public", Enabled: true, PrimaryModel: "model"},
		{ID: 2, Name: "private", Enabled: true, AdminOnly: true, PrimaryModel: "model"},
	}}
	svc := NewChannelMonitorService(repo, nil)
	// Alternate roles against the exact same repository slice to detect either
	// administrator leakage or destructive filtering that hides future admin rows.
	for _, admin := range []bool{true, false, true, false} {
		repo.queried = nil
		views, err := svc.ListUserView(context.Background(), admin)
		require.NoError(t, err)
		if admin {
			require.Len(t, views, 2)
			require.True(t, views[1].AdminOnly)
		} else {
			require.Len(t, views, 1)
			require.EqualValues(t, 1, views[0].ID)
			for _, ids := range repo.queried {
				require.Equal(t, []int64{1}, ids, "private history/quota must not be fetched")
			}
		}
	}
	views, err := svc.ListUserView(context.Background())
	require.NoError(t, err)
	require.Len(t, views, 1, "old callers and public transit default to ordinary visibility")
	require.Len(t, repo.monitors, 2)
}

func TestChannelMonitorAdminOnlyDirectDetailDeniedBeforeHistory(t *testing.T) {
	repo := &adminOnlyMonitorRepo{monitors: []*ChannelMonitor{{ID: 2, Enabled: true, AdminOnly: true, PrimaryModel: "model"}}}
	svc := NewChannelMonitorService(repo, nil)
	_, err := svc.GetUserDetail(context.Background(), 2)
	require.ErrorIs(t, err, ErrChannelMonitorNotFound)
	require.Zero(t, repo.details)
	_, err = svc.GetUserDetail(context.Background(), 2, false)
	require.ErrorIs(t, err, ErrChannelMonitorNotFound)
	require.Zero(t, repo.details)
	detail, err := svc.GetUserDetail(context.Background(), 2, true)
	require.NoError(t, err)
	require.True(t, detail.AdminOnly)
	require.Equal(t, 4, repo.details)
	// An updated private flag is checked on each request, after admin access.
	_, err = svc.GetUserDetail(context.Background(), 2, false)
	require.ErrorIs(t, err, ErrChannelMonitorNotFound)
	require.Equal(t, 4, repo.details)
}

func TestChannelMonitorAdminOnlyUpdatePreservesMissingAndClearsExplicitFalse(t *testing.T) {
	monitor := &ChannelMonitor{AdminOnly: true, Provider: MonitorProviderOpenAI, APIMode: MonitorAPIModeChatCompletions,
		Endpoint: "https://8.8.8.8", PrimaryModel: "model", IntervalSeconds: 60}
	require.NoError(t, applyMonitorUpdate(monitor, ChannelMonitorUpdateParams{}))
	require.True(t, monitor.AdminOnly)
	no := false
	require.NoError(t, applyMonitorUpdate(monitor, ChannelMonitorUpdateParams{AdminOnly: &no}))
	require.False(t, monitor.AdminOnly)
	yes := true
	require.NoError(t, applyMonitorUpdate(monitor, ChannelMonitorUpdateParams{AdminOnly: &yes}))
	require.True(t, monitor.AdminOnly)
}
