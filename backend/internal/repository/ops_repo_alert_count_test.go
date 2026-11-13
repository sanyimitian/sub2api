package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsCountErrorsByStatusUsesListScopeAndRequestedModelExclusions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	start, end := time.Now().Add(-5*time.Minute), time.Now()
	groupID := int64(7)
	filter := &service.OpsErrorLogFilter{StartTime: &start, EndTime: &end, Platform: "openai", GroupID: &groupID,
		Phase: "upstream", IncludeRecoveredUpstream: true, StatusCodes: []int{502, 503}, ExcludedModels: []string{"model-a", "model%"}}
	query := `SELECT COALESCE(e.upstream_status_code, e.status_code, 0), COUNT(*)
FROM ops_error_logs e WHERE 1=1 AND e.created_at >= $1 AND e.created_at < $2 AND e.platform = $3 AND e.group_id = $4 AND e.error_phase = $5 AND COALESCE(e.is_business_limited,false) = false AND COALESCE(e.upstream_status_code, e.status_code, 0) = ANY($6) AND NOT (COALESCE(NULLIF(e.requested_model, ''), e.model, '') = ANY($7)) GROUP BY 1`
	mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(start.UTC(), end.UTC(), "openai", int64(7), "upstream", "{502,503}", `{"model-a","model%"}`).
		WillReturnRows(sqlmock.NewRows([]string{"status", "count"}).AddRow(502, 12).AddRow(503, 9))
	repo := &opsRepository{db: db}
	counts, err := repo.CountErrorsByStatus(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, map[int]int64{502: 12, 503: 9}, counts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsPendingNotificationsIncludeRecoveredAlerts(t *testing.T) {
	where, _ := buildOpsAlertEventsWhere(&service.OpsAlertEventFilter{PendingNotifications: true})
	require.Contains(t, where, "jsonb_each")
	require.Contains(t, where, "IN ('pending', 'failed')")
	require.Contains(t, where, "< 3")
	require.Contains(t, where, "<= NOW()")
	require.NotContains(t, where, "status = $", "recovered incidents still need their failed notifications delivered")
}
