package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.DownstreamAssessmentRepository = (*usageLogRepository)(nil)

// Explicit grants include users who have never made a request. API keys and
// current subscriptions also establish membership; public access alone does not.
// EXISTS prevents multiple keys/grants from multiplying users or token counts.
// The group selects members only. Count their usage across ALL groups (including
// logs without a group) within the date range, regardless of current key bindings.
// Last-used time follows user management: all retained usage, independent of the
// assessment dates and group. Fetch it only for users on the requested page.
const downstreamAssessmentSQL = `
WITH members AS (
    SELECT u.id, u.email, COALESCE(u.username, '') AS username
    FROM users u
    WHERE u.deleted_at IS NULL AND (
        EXISTS (SELECT 1 FROM user_allowed_groups ag WHERE ag.user_id = u.id AND ag.group_id = $1)
        OR EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = u.id AND k.group_id = $1 AND k.deleted_at IS NULL)
        OR EXISTS (SELECT 1 FROM user_subscriptions s WHERE s.user_id = u.id AND s.group_id = $1
            AND s.deleted_at IS NULL AND s.status = 'active' AND s.starts_at <= NOW() AND s.expires_at > NOW())
    )
), usage AS (
    SELECT ul.user_id, COUNT(*) AS total_requests,
        COALESCE(SUM(ul.input_tokens::bigint + ul.output_tokens::bigint
            + ul.cache_creation_tokens::bigint + ul.cache_read_tokens::bigint), 0) AS total_tokens
    FROM usage_logs ul JOIN members m ON m.id = ul.user_id
    WHERE ul.created_at >= $2 AND ul.created_at < $3
    GROUP BY ul.user_id
), assessed AS (
    SELECT m.id AS user_id, m.email, m.username, COALESCE(u.total_requests, 0) AS total_requests,
        COALESCE(u.total_tokens, 0) AS total_tokens,
        COALESCE(u.total_tokens, 0)::numeric / $4::numeric AS daily_average,
        COALESCE(u.total_tokens, 0)::numeric >= $5::numeric * $4::numeric AS qualified
    FROM members m LEFT JOIN usage u ON u.user_id = m.id
), summary AS (
    SELECT COUNT(*) AS total, COUNT(*) FILTER (WHERE qualified) AS qualified_count,
        COUNT(*) FILTER (WHERE NOT qualified) AS unqualified_count,
        COALESCE(SUM(total_tokens), 0) AS total_tokens
    FROM assessed
), page AS (
    SELECT * FROM assessed ORDER BY qualified ASC, %s, user_id ASC LIMIT $6 OFFSET $7
)
SELECT s.total, s.qualified_count, s.unqualified_count, s.total_tokens AS summary_total_tokens,
    p.user_id, p.email, p.username, p.total_requests, p.total_tokens, p.daily_average, p.qualified,
    last_usage.created_at AS last_used_at
FROM summary s LEFT JOIN page p ON TRUE
LEFT JOIN LATERAL (
    SELECT ul.created_at FROM usage_logs ul
    WHERE ul.user_id = p.user_id
    ORDER BY ul.created_at DESC LIMIT 1
) last_usage ON TRUE
ORDER BY p.qualified ASC, p.%s, p.user_id ASC`

func downstreamAssessmentQuery(filter service.DownstreamAssessmentFilter) string {
	// Only interpolate fixed identifiers/directions, never raw request parameters.
	column, direction := "total_tokens", "ASC"
	if filter.SortBy == "daily_average" {
		column = "daily_average"
	}
	if filter.SortOrder == "desc" {
		direction = "DESC"
	}
	order := column + " " + direction
	// Apply the same order before pagination and after joining last-used times.
	return fmt.Sprintf(downstreamAssessmentSQL, order, order)
}

func (r *usageLogRepository) GetDownstreamAssessment(ctx context.Context, params pagination.PaginationParams, filter service.DownstreamAssessmentFilter) (*service.DownstreamAssessmentResult, error) {
	rows, err := r.sql.QueryContext(ctx, downstreamAssessmentQuery(filter), filter.GroupID, filter.StartTime, filter.EndTime, filter.Days, filter.DailyThreshold, params.Limit(), params.Offset())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &service.DownstreamAssessmentResult{
		Items: make([]service.DownstreamAssessmentUser, 0),
		Days:  filter.Days, DailyThreshold: filter.DailyThreshold, Page: params.Page, PageSize: params.Limit(),
	}
	for rows.Next() {
		var userID, requests, tokens sql.NullInt64
		var email, username sql.NullString
		var average sql.NullFloat64
		var qualified sql.NullBool
		var lastUsedAt sql.NullTime
		if err := rows.Scan(&result.Total, &result.QualifiedCount, &result.UnqualifiedCount, &result.TotalTokens,
			&userID, &email, &username, &requests, &tokens, &average, &qualified, &lastUsedAt); err != nil {
			return nil, err
		}
		if userID.Valid {
			var lastUsed *time.Time
			if lastUsedAt.Valid {
				ts := lastUsedAt.Time.UTC()
				lastUsed = &ts
			}
			result.Items = append(result.Items, service.DownstreamAssessmentUser{
				UserID: userID.Int64, Email: email.String, Username: username.String,
				TotalRequests: requests.Int64, TotalTokens: tokens.Int64, DailyAverage: average.Float64, Qualified: qualified.Bool,
				LastUsedAt: lastUsed,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result.Pages = int((result.Total + int64(result.PageSize) - 1) / int64(result.PageSize))
	return result, nil
}
