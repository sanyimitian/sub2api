package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// GetPublicTransitPassiveAggregates returns privacy-safe request aggregates
// from successful usage rows and recorded gateway errors. It intentionally
// returns no user/account/key/channel identifiers; group IDs are used only to
// join the public group name and are discarded by the service DTO layer.
func (r *opsRepository) GetPublicTransitPassiveAggregates(ctx context.Context, since, until time.Time, bucketSeconds int64) ([]service.PublicTransitPassiveAggregate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if since.IsZero() || until.IsZero() || since.After(until) {
		return nil, fmt.Errorf("invalid passive monitoring window")
	}
	if bucketSeconds <= 0 {
		return nil, fmt.Errorf("invalid passive monitoring bucket")
	}
	q := `
WITH successes AS (
  SELECT
	to_timestamp(floor(extract(epoch FROM ul.created_at) / $3) * $3) AS bucket_start,
    CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(g.platform, ''), a.platform) END AS platform,
    ul.group_id,
    COALESCE(g.name, '') AS group_name,
    COALESCE(NULLIF(ul.requested_model, ''), ul.model) AS model,
    COUNT(*)::bigint AS success_count,
    SUM(CASE WHEN ul.duration_ms IS NOT NULL THEN ul.duration_ms ELSE 0 END)::bigint AS latency_sum,
    COUNT(ul.duration_ms)::bigint AS latency_samples,
    SUM(CASE WHEN ul.first_token_ms IS NOT NULL THEN ul.first_token_ms ELSE 0 END)::bigint AS ttft_sum,
    COUNT(ul.first_token_ms)::bigint AS ttft_samples,
	COALESCE(SUM(ul.input_tokens), 0)::bigint AS input_tokens,
	COALESCE(SUM(ul.cache_creation_tokens), 0)::bigint AS cache_creation_tokens,
	COALESCE(SUM(ul.cache_read_tokens), 0)::bigint AS cache_read_tokens,
    MAX(ul.created_at) AS last_request_at
  FROM usage_logs ul
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= $1 AND ul.created_at < $2
    AND (ul.group_id IS NULL OR (COALESCE(NULLIF(g.status, ''), 'active') = 'active' AND COALESCE(g.is_exclusive, false) = false))
  GROUP BY bucket_start, CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(g.platform, ''), a.platform) END, ul.group_id, g.name, COALESCE(NULLIF(ul.requested_model, ''), ul.model)
), errors AS (
  SELECT
	to_timestamp(floor(extract(epoch FROM oel.created_at) / $3) * $3) AS bucket_start,
    CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(oel.platform, ''), NULLIF(g.platform, ''), a.platform) END AS platform,
    oel.group_id,
    COALESCE(g.name, '') AS group_name,
    COALESCE(NULLIF(oel.requested_model, ''), NULLIF(oel.model, ''), '') AS model,
    COUNT(*)::bigint AS error_count,
    SUM(CASE WHEN oel.duration_ms IS NOT NULL THEN oel.duration_ms ELSE 0 END)::bigint AS latency_sum,
    COUNT(oel.duration_ms)::bigint AS latency_samples,
    SUM(CASE WHEN oel.time_to_first_token_ms IS NOT NULL THEN oel.time_to_first_token_ms ELSE 0 END)::bigint AS ttft_sum,
    COUNT(oel.time_to_first_token_ms)::bigint AS ttft_samples,
    MAX(oel.created_at) AS last_request_at
  FROM ops_error_logs oel
  LEFT JOIN groups g ON g.id = oel.group_id
  LEFT JOIN accounts a ON a.id = oel.account_id
  WHERE oel.created_at >= $1 AND oel.created_at < $2
    AND COALESCE(oel.status_code, 0) >= 400
    AND (oel.group_id IS NULL OR (COALESCE(NULLIF(g.status, ''), 'active') = 'active' AND COALESCE(g.is_exclusive, false) = false))
  GROUP BY bucket_start, CASE WHEN g.platform = 'composite' THEN a.platform ELSE COALESCE(NULLIF(oel.platform, ''), NULLIF(g.platform, ''), a.platform) END, oel.group_id, g.name, COALESCE(NULLIF(oel.requested_model, ''), NULLIF(oel.model, ''), '')
)
SELECT
	COALESCE(s.bucket_start, e.bucket_start) AS bucket_start,
  COALESCE(s.platform, e.platform) AS platform,
  COALESCE(s.group_id, e.group_id) AS group_id,
  COALESCE(NULLIF(s.group_name, ''), NULLIF(e.group_name, ''), '') AS group_name,
  COALESCE(s.model, e.model) AS model,
  COALESCE(s.success_count, 0)::bigint + COALESCE(e.error_count, 0)::bigint AS request_count,
  COALESCE(s.success_count, 0)::bigint AS success_count,
  COALESCE(e.error_count, 0)::bigint AS error_count,
  COALESCE(s.latency_sum, 0)::bigint + COALESCE(e.latency_sum, 0)::bigint AS latency_sum,
  COALESCE(s.latency_samples, 0)::bigint + COALESCE(e.latency_samples, 0)::bigint AS latency_samples,
  COALESCE(s.ttft_sum, 0)::bigint + COALESCE(e.ttft_sum, 0)::bigint AS ttft_sum,
  COALESCE(s.ttft_samples, 0)::bigint + COALESCE(e.ttft_samples, 0)::bigint AS ttft_samples,
	COALESCE(s.input_tokens, 0)::bigint AS input_tokens,
	COALESCE(s.cache_creation_tokens, 0)::bigint AS cache_creation_tokens,
	COALESCE(s.cache_read_tokens, 0)::bigint AS cache_read_tokens,
  NULLIF(GREATEST(COALESCE(s.last_request_at, '-infinity'::timestamptz), COALESCE(e.last_request_at, '-infinity'::timestamptz)), '-infinity'::timestamptz) AS last_request_at
FROM successes s
FULL OUTER JOIN errors e
  ON s.bucket_start = e.bucket_start
 AND s.platform = e.platform
 AND s.group_id IS NOT DISTINCT FROM e.group_id
 AND s.model = e.model
ORDER BY platform, model, group_name, bucket_start`

	rows, err := r.db.QueryContext(ctx, q, since.UTC(), until.UTC(), bucketSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]service.PublicTransitPassiveAggregate, 0)
	for rows.Next() {
		var bucketStart time.Time
		var platform, groupName, model string
		var groupID sql.NullInt64
		var requestCount, successCount, errorCount, latencySum, latencySamples, ttftSum, ttftSamples int64
		var inputTokens, cacheCreationTokens, cacheReadTokens int64
		var lastRequestAt sql.NullTime
		if err := rows.Scan(&bucketStart, &platform, &groupID, &groupName, &model, &requestCount, &successCount, &errorCount, &latencySum, &latencySamples, &ttftSum, &ttftSamples, &inputTokens, &cacheCreationTokens, &cacheReadTokens, &lastRequestAt); err != nil {
			return nil, err
		}
		platform = strings.TrimSpace(platform)
		model = strings.TrimSpace(model)
		if platform == "" || model == "" {
			continue
		}
		var gid *int64
		if groupID.Valid && groupID.Int64 != 0 {
			v := groupID.Int64
			gid = &v
		}
		var last *time.Time
		if lastRequestAt.Valid {
			v := lastRequestAt.Time.UTC()
			last = &v
		}
		out = append(out, service.PublicTransitPassiveAggregate{
			BucketStart: &bucketStart, Platform: platform, GroupID: gid, GroupName: strings.TrimSpace(groupName), Model: model,
			RequestCount: requestCount, SuccessCount: successCount, ErrorCount: errorCount,
			TotalLatencyMs: latencySum, LatencySamples: latencySamples,
			TotalTTFTMs: ttftSum, TTFTSamples: ttftSamples,
			InputTokens: inputTokens, CacheCreate: cacheCreationTokens, CacheRead: cacheReadTokens,
			LastRequestAt: last,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
