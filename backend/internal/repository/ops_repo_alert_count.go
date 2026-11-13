package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *opsRepository) CountErrorsByStatus(ctx context.Context, filter *service.OpsErrorLogFilter) (map[int]int64, error) {
	if filter == nil || filter.StartTime == nil || filter.EndTime == nil {
		return nil, fmt.Errorf("error count requires a bounded time window")
	}
	where, args := buildOpsErrorLogsWhere(filter)
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(e.upstream_status_code, e.status_code, 0), COUNT(*)
FROM ops_error_logs e `+where+` GROUP BY 1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[int]int64{}
	for rows.Next() {
		var code int
		var count int64
		if err := rows.Scan(&code, &count); err != nil {
			return nil, err
		}
		counts[code] = count
	}
	return counts, rows.Err()
}

func (r *opsRepository) UpdateAlertDeliveries(ctx context.Context, eventID int64, deliveries map[string]*service.OpsAlertDelivery) error {
	raw, err := json.Marshal(deliveries)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE ops_alert_events
SET dimensions = jsonb_set(COALESCE(dimensions, '{}'::jsonb), '{notifications}', $2::jsonb)
WHERE id = $1`, eventID, string(raw))
	return err
}
