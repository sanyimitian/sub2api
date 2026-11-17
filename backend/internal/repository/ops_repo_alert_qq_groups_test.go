package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsAlertRuleQQGroupsPersistence(t *testing.T) {
	for _, groups := range [][]string{nil, {"GROUP_A", "GROUP_B"}} {
		t.Run(fmt.Sprint(groups), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := &opsRepository{db: db}
			rule := &service.OpsAlertRule{ID: 1, Name: "QQ alert", Enabled: true, Severity: "P1", MetricType: "error_rate",
				Operator: ">", Threshold: 1, WindowMinutes: 1, SustainedMinutes: 2, CooldownMinutes: 10,
				NotifyQQ: true, QQGroupOpenIDs: groups}
			array := "{}"
			var arrayArg any
			if len(groups) > 0 {
				array = `{"GROUP_A","GROUP_B"}`
				arrayArg = array
			}
			rows := func() *sqlmock.Rows {
				return sqlmock.NewRows([]string{"id", "name", "description", "enabled", "severity", "metric_type", "operator", "threshold",
					"window_minutes", "sustained_minutes", "cooldown_minutes", "notify_email", "notify_qq", "qq_group_openids", "filters",
					"last_triggered_at", "created_at", "updated_at"}).
					AddRow(1, "QQ alert", "", true, "P1", "error_rate", ">", 1, 1, 2, 10, false, true, array, nil, nil, time.Now(), time.Now())
			}
			mock.ExpectQuery(`INSERT INTO ops_alert_rules \([\s\S]*qq_group_openids`).
				WithArgs("QQ alert", "", true, "P1", "error_rate", ">", float64(1), 1, 2, 10, false, true, arrayArg, nil).WillReturnRows(rows())
			created, err := repo.CreateAlertRule(context.Background(), rule)
			require.NoError(t, err)
			require.Equal(t, groups, nilIfEmptyQQGroups(created.QQGroupOpenIDs))
			mock.ExpectQuery(`UPDATE ops_alert_rules[\s\S]*qq_group_openids = COALESCE\(\$14`).
				WithArgs(int64(1), "QQ alert", "", true, "P1", "error_rate", ">", float64(1), 1, 2, 10, false, true, arrayArg, nil).WillReturnRows(rows())
			updated, err := repo.UpdateAlertRule(context.Background(), rule)
			require.NoError(t, err)
			require.Equal(t, groups, nilIfEmptyQQGroups(updated.QQGroupOpenIDs))
			mock.ExpectQuery(`SELECT[\s\S]*qq_group_openids[\s\S]*FROM ops_alert_rules`).WillReturnRows(rows())
			listed, err := repo.ListAlertRules(context.Background())
			require.NoError(t, err)
			require.Len(t, listed, 1)
			require.Equal(t, groups, nilIfEmptyQQGroups(listed[0].QQGroupOpenIDs))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func nilIfEmptyQQGroups(groups []string) []string {
	if len(groups) == 0 {
		return nil
	}
	return groups
}
