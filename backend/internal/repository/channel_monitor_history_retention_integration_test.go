//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/channelmonitorhistory"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func newMonitorHistoryRetentionFixture(t *testing.T) (service.ChannelMonitorRepository, int64) {
	t.Helper()
	ctx := context.Background()
	repo := NewChannelMonitorRepository(integrationEntClient, integrationDB)
	monitor := &service.ChannelMonitor{
		Name: "history-retention", Provider: service.MonitorProviderOpenAI,
		Endpoint: "https://api.openai.com", APIKey: "encrypted", PrimaryModel: "primary",
		ExtraModels: []string{"extra"}, Enabled: true, IntervalSeconds: 60,
	}
	require.NoError(t, repo.Create(ctx, monitor))
	t.Cleanup(func() { require.NoError(t, repo.Delete(ctx, monitor.ID)) })
	return repo, monitor.ID
}

func seedMonitorHistory(t *testing.T, monitorID int64, model, status, message string, checkedAt time.Time) {
	t.Helper()
	// 绕过新写入策略，模拟升级前数据库内的旧记录。
	_, err := integrationEntClient.ChannelMonitorHistory.Create().
		SetMonitorID(monitorID).SetModel(model).
		SetStatus(channelmonitorhistory.Status(status)).SetMessage(message).
		SetCheckedAt(checkedAt).Save(context.Background())
	require.NoError(t, err)
}

func requireMonitorHistoryRetention(t *testing.T, repo service.ChannelMonitorRepository, monitorID int64, wantMessages []string, normalCount int) {
	t.Helper()
	history, err := repo.ListHistory(context.Background(), monitorID, "", 100)
	require.NoError(t, err)
	var abnormal []*service.ChannelMonitorHistoryEntry
	var normals int
	for _, row := range history {
		if row.Status == service.MonitorStatusOperational {
			normals++
		} else {
			abnormal = append(abnormal, row)
		}
	}
	require.Equal(t, normalCount, normals)
	require.Len(t, abnormal, len(wantMessages))
	for i, message := range wantMessages {
		require.Equal(t, message, abnormal[i].Message)
	}
}

func TestChannelMonitorHistoryRetainsOnlyLatestAbnormalAcrossModels(t *testing.T) {
	ctx := context.Background()
	repo, monitorID := newMonitorHistoryRetentionFixture(t)
	_, otherID := newMonitorHistoryRetentionFixture(t)
	base := time.Now().UTC().Truncate(time.Second)
	seedMonitorHistory(t, monitorID, "primary", service.MonitorStatusOperational, "old normal", base)
	seedMonitorHistory(t, monitorID, "primary", service.MonitorStatusDegraded, "old degraded", base.Add(time.Second))
	seedMonitorHistory(t, monitorID, "extra", service.MonitorStatusFailed, "old failed", base.Add(2*time.Second))
	seedMonitorHistory(t, monitorID, "primary", service.MonitorStatusError, "old timeout", base.Add(3*time.Second))
	seedMonitorHistory(t, otherID, "primary", service.MonitorStatusDegraded, "other degraded", base)
	seedMonitorHistory(t, otherID, "extra", service.MonitorStatusFailed, "other failed", base)

	// 首次正常更新也会清理旧异常，保留最近的超时和所有正常记录。
	require.NoError(t, repo.InsertHistoryBatch(ctx, []*service.ChannelMonitorHistoryRow{{
		MonitorID: monitorID, Model: "primary", Status: service.MonitorStatusOperational,
		Message: "new normal", CheckedAt: base.Add(4 * time.Second),
	}}))
	requireMonitorHistoryRetention(t, repo, monitorID, []string{"old timeout", "old failed"}, 2)

	// 新异常跨模型替换旧异常；错误也参与同一条保留规则。
	for i, status := range []string{service.MonitorStatusDegraded, service.MonitorStatusFailed, service.MonitorStatusError} {
		require.NoError(t, repo.InsertHistoryBatch(ctx, []*service.ChannelMonitorHistoryRow{{
			MonitorID: monitorID, Model: "extra", Status: status,
			Message: status, CheckedAt: base.Add(time.Duration(5+i) * time.Second),
		}}))
		want := []string{status, "old timeout"}
		if i > 0 {
			want[1] = []string{service.MonitorStatusDegraded, service.MonitorStatusFailed}[i-1]
		}
		requireMonitorHistoryRetention(t, repo, monitorID, want, 2)
	}

	// 迟到的旧异常不能覆盖更晚发生的异常；恢复正常仍保留最新异常。
	require.NoError(t, repo.InsertHistoryBatch(ctx, []*service.ChannelMonitorHistoryRow{
		{MonitorID: monitorID, Model: "primary", Status: service.MonitorStatusDegraded, Message: "late old result", CheckedAt: base},
		{MonitorID: monitorID, Model: "primary", Status: service.MonitorStatusOperational, CheckedAt: base.Add(8 * time.Second)},
	}))
	requireMonitorHistoryRetention(t, repo, monitorID, []string{service.MonitorStatusError, service.MonitorStatusFailed}, 3)
	otherHistory, err := repo.ListHistory(ctx, otherID, "", 100)
	require.NoError(t, err)
	require.Len(t, otherHistory, 2, "更新一个监控不能清理另一个监控")

	// 同一时刻多个模型异常，以后插入的 ID 较大记录打破平局。
	tiedAt := base.Add(9 * time.Second)
	require.NoError(t, repo.InsertHistoryBatch(ctx, []*service.ChannelMonitorHistoryRow{
		{MonitorID: monitorID, Model: "primary", Status: service.MonitorStatusFailed, Message: "tied first", CheckedAt: tiedAt},
		{MonitorID: monitorID, Model: "extra", Status: service.MonitorStatusDegraded, Message: "tied last", CheckedAt: tiedAt},
	}))
	requireMonitorHistoryRetention(t, repo, monitorID, []string{"tied last", "tied first"}, 3)
}

func TestChannelMonitorHistoryConcurrentUpdatesRetainLatestAbnormal(t *testing.T) {
	repo, monitorID := newMonitorHistoryRetentionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	base := time.Now().UTC().Truncate(time.Second)
	var group errgroup.Group
	for i := 0; i < 8; i++ {
		group.Go(func() error {
			return repo.InsertHistoryBatch(ctx, []*service.ChannelMonitorHistoryRow{
				{MonitorID: monitorID, Model: "primary", Status: service.MonitorStatusOperational, CheckedAt: base},
				{MonitorID: monitorID, Model: "extra", Status: service.MonitorStatusFailed,
					Message: fmt.Sprintf("failure-%d", i), CheckedAt: base.Add(time.Duration(i) * time.Second)},
			})
		})
	}
	require.NoError(t, group.Wait())
	requireMonitorHistoryRetention(t, repo, monitorID, []string{"failure-7", "failure-6"}, 8)
}

func TestChannelMonitorHistoryRetentionUsesCallerTransaction(t *testing.T) {
	repo, monitorID := newMonitorHistoryRetentionFixture(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	seedMonitorHistory(t, monitorID, "primary", service.MonitorStatusFailed, "original failure", base)
	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	require.NoError(t, repo.InsertHistoryBatch(dbent.NewTxContext(ctx, tx), []*service.ChannelMonitorHistoryRow{{
		MonitorID: monitorID, Model: "extra", Status: service.MonitorStatusDegraded,
		Message: "rolled back result", CheckedAt: base.Add(time.Second),
	}}))
	require.NoError(t, tx.Rollback())
	requireMonitorHistoryRetention(t, repo, monitorID, []string{"original failure"}, 0)
}

func TestChannelMonitorHistoryRetentionNormalBatchAndFailedWrite(t *testing.T) {
	repo, firstID := newMonitorHistoryRetentionFixture(t)
	_, secondID := newMonitorHistoryRetentionFixture(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.InsertHistoryBatch(ctx, nil))
	require.NoError(t, repo.InsertHistoryBatch(ctx, []*service.ChannelMonitorHistoryRow{
		{MonitorID: firstID, Model: "primary", Status: service.MonitorStatusOperational, CheckedAt: base},
		{MonitorID: secondID, Model: "primary", Status: service.MonitorStatusFailed, Message: "second failure", CheckedAt: base},
	}))
	firstHistory, err := repo.ListHistory(ctx, firstID, "", 100)
	require.NoError(t, err)
	require.Len(t, firstHistory, 1)
	require.Equal(t, service.MonitorStatusOperational, firstHistory[0].Status)
	requireMonitorHistoryRetention(t, repo, secondID, []string{"second failure"}, 0)

	// 使整批插入违反外键约束，确认失败不会先删除已有异常或留下部分新记录。
	err = repo.InsertHistoryBatch(ctx, []*service.ChannelMonitorHistoryRow{
		{MonitorID: secondID, Model: "extra", Status: service.MonitorStatusDegraded,
			Message: "must roll back", CheckedAt: base.Add(time.Second)},
		{MonitorID: -1, Model: "primary", Status: service.MonitorStatusFailed, CheckedAt: base},
	})
	require.Error(t, err)
	requireMonitorHistoryRetention(t, repo, secondID, []string{"second failure"}, 0)
}
