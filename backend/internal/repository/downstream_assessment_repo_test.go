//go:build unit

package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var assessmentColumns = []string{"total", "qualified_count", "unqualified_count", "summary_tokens", "user_id", "email", "username", "total_requests", "total_tokens", "daily_average", "qualified", "last_used_at"}

func TestDownstreamAssessmentRepositoryPagination(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	lastUsed := time.Date(2026, 10, 15, 12, 34, 56, 0, time.FixedZone("UTC+8", 8*60*60))
	filter := service.DownstreamAssessmentFilter{GroupID: 7, StartTime: start, EndTime: start.AddDate(0, 0, 7), Days: 7, DailyThreshold: 1000000000}
	mock.ExpectQuery(regexp.QuoteMeta(downstreamAssessmentSQL)).
		WithArgs(int64(7), filter.StartTime, filter.EndTime, 7, int64(1000000000), 2, 0).
		WillReturnRows(sqlmock.NewRows(assessmentColumns).
			AddRow(3, 1, 2, int64(7000000000), 1, "idle@test.com", "Idle", 0, 0, 0, false, nil).
			AddRow(3, 1, 2, int64(7000000000), 2, "met@test.com", "Met", 7, int64(7000000000), 1000000000, true, lastUsed))
	result, err := repo.GetDownstreamAssessment(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 2}, filter)
	require.NoError(t, err)
	require.Equal(t, int64(3), result.Total)
	require.Equal(t, 2, result.Pages)
	require.Equal(t, int64(2), result.UnqualifiedCount)
	require.Len(t, result.Items, 2)
	require.Zero(t, result.Items[0].TotalTokens)
	require.False(t, result.Items[0].Qualified)
	require.Nil(t, result.Items[0].LastUsedAt)
	require.True(t, result.Items[1].Qualified)
	require.Equal(t, int64(7000000000), result.Items[1].TotalTokens)
	require.NotNil(t, result.Items[1].LastUsedAt)
	require.Equal(t, lastUsed.UTC(), *result.Items[1].LastUsedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDownstreamAssessmentRepositoryEmptyPageKeepsSummary(t *testing.T) {
	for _, total := range []int64{0, 7} {
		db, mock := newSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		mock.ExpectQuery(regexp.QuoteMeta(downstreamAssessmentSQL)).WillReturnRows(sqlmock.NewRows(assessmentColumns).AddRow(total, 0, total, 0, nil, nil, nil, nil, nil, nil, nil, nil))
		result, err := repo.GetDownstreamAssessment(context.Background(), pagination.PaginationParams{Page: 5, PageSize: 2}, service.DownstreamAssessmentFilter{Days: 7, DailyThreshold: 1})
		require.NoError(t, err)
		require.Equal(t, total, result.Total)
		require.NotNil(t, result.Items)
		require.Empty(t, result.Items)
		require.Equal(t, 5, result.Page)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestDownstreamAssessmentRepositoryErrors(t *testing.T) {
	for _, stage := range []string{"query", "scan", "iteration"} {
		t.Run(stage, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := newUsageLogRepositoryWithSQL(nil, db)
			expect := mock.ExpectQuery(regexp.QuoteMeta(downstreamAssessmentSQL))
			switch stage {
			case "query":
				expect.WillReturnError(errors.New("unavailable"))
			case "scan":
				expect.WillReturnRows(sqlmock.NewRows(assessmentColumns).AddRow("bad", 0, 0, 0, nil, nil, nil, nil, nil, nil, nil, nil))
			case "iteration":
				expect.WillReturnRows(sqlmock.NewRows(assessmentColumns).AddRow(0, 0, 0, 0, nil, nil, nil, nil, nil, nil, nil, nil).RowError(0, errors.New("interrupted")))
			}
			_, err := repo.GetDownstreamAssessment(context.Background(), pagination.PaginationParams{Page: 1, PageSize: 20}, service.DownstreamAssessmentFilter{Days: 1, DailyThreshold: 1})
			require.Error(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
