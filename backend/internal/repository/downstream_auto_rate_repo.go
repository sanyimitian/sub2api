package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *usageLogRepository) GetDownstreamAutoRateConfig(ctx context.Context) (*service.DownstreamAutoRateConfig, error) {
	var enabled bool
	var mode string
	var groupIDs []int64
	var tiersJSON []byte
	var lastRun sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT enabled, mode, group_ids, tiers, last_run_date FROM downstream_auto_rate_config WHERE id = TRUE`).Scan(&enabled, &mode, pq.Array(&groupIDs), &tiersJSON, &lastRun)
	if err != nil {
		return nil, err
	}
	var tiers []service.DownstreamAutoRateTier
	if err := json.Unmarshal(tiersJSON, &tiers); err != nil {
		return nil, err
	}
	cfg := &service.DownstreamAutoRateConfig{Enabled: enabled, Mode: mode, GroupIDs: groupIDs, Tiers: tiers}
	if cfg.GroupIDs == nil {
		cfg.GroupIDs = []int64{}
	}
	if lastRun.Valid {
		date := lastRun.Time.Format("2006-01-02")
		cfg.LastRunDate = &date
	}
	return cfg, nil
}

func (r *usageLogRepository) SaveDownstreamAutoRateConfig(ctx context.Context, cfg service.DownstreamAutoRateConfig) error {
	tiersJSON, err := json.Marshal(cfg.Tiers)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM groups WHERE id = ANY($1) AND deleted_at IS NULL`, pq.Array(cfg.GroupIDs)).Scan(&count); err != nil {
		return err
	}
	if count != len(cfg.GroupIDs) {
		return fmt.Errorf("selected group does not exist")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE downstream_auto_rate_config SET enabled = $1, mode = $2, group_ids = $3, tiers = $4, updated_at = NOW(), last_run_date = NULL WHERE id = TRUE`, cfg.Enabled, cfg.Mode, pq.Array(cfg.GroupIDs), tiersJSON); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM downstream_auto_user_group_rates WHERE group_id <> ALL($1) OR $2 = FALSE`, pq.Array(cfg.GroupIDs), cfg.Enabled); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *usageLogRepository) ApplyDownstreamAutoRates(ctx context.Context, endDate time.Time, workingDays []time.Time, cfg service.DownstreamAutoRateConfig) (bool, error) {
	if len(workingDays) == 0 || (cfg.Mode == service.DownstreamAutoRateWeekly && endDate.Weekday() != time.Friday) ||
		(cfg.Mode != service.DownstreamAutoRateDaily && cfg.Mode != service.DownstreamAutoRateWeekly) {
		return false, fmt.Errorf("invalid automatic rate period")
	}
	dateStrings := make([]string, 0, len(workingDays))
	for _, day := range workingDays {
		dateStrings = append(dateStrings, day.Format("2006-01-02"))
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var enabled bool
	var mode string
	var groupIDs []int64
	var tiersJSON []byte
	var lastRun sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT enabled, mode, group_ids, tiers, last_run_date FROM downstream_auto_rate_config WHERE id = TRUE FOR UPDATE`).Scan(&enabled, &mode, pq.Array(&groupIDs), &tiersJSON, &lastRun); err != nil {
		return false, err
	}
	if !enabled || mode != cfg.Mode || (lastRun.Valid && lastRun.Time.Format("2006-01-02") == endDate.Format("2006-01-02")) {
		return false, nil
	}
	var tiers []service.DownstreamAutoRateTier
	if err := json.Unmarshal(tiersJSON, &tiers); err != nil {
		return false, err
	}
	if len(tiers) == 0 {
		return false, fmt.Errorf("no automatic rate tiers configured")
	}
	start := endDate
	if mode == service.DownstreamAutoRateWeekly {
		start = endDate.AddDate(0, 0, -6)
	}
	end := endDate.AddDate(0, 0, 1)
	// Full replacement lets users below the first tier return to the group default.
	// The manual rate table remains independent and always wins on reads.
	_, err = tx.ExecContext(ctx, `DELETE FROM downstream_auto_user_group_rates`)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `
		WITH members AS (
			SELECT DISTINCT g.group_id, u.id AS user_id
			FROM unnest($1::bigint[]) AS g(group_id)
			JOIN groups gr ON gr.id = g.group_id AND gr.deleted_at IS NULL
			JOIN users u ON u.deleted_at IS NULL AND (
				EXISTS (SELECT 1 FROM user_allowed_groups ag WHERE ag.user_id = u.id AND ag.group_id = g.group_id)
				OR EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = u.id AND k.group_id = g.group_id AND k.deleted_at IS NULL)
				OR EXISTS (SELECT 1 FROM user_subscriptions s WHERE s.user_id = u.id AND s.group_id = g.group_id AND s.deleted_at IS NULL AND s.status = 'active' AND s.starts_at <= NOW() AND s.expires_at > NOW())
			)
		), usage AS (
			SELECT ul.user_id, SUM(ul.input_tokens::bigint + ul.output_tokens::bigint + ul.cache_creation_tokens::bigint + ul.cache_read_tokens::bigint) AS tokens
			FROM usage_logs ul
			WHERE ul.created_at >= $2 AND ul.created_at < $3
			  AND (ul.created_at AT TIME ZONE $5)::date = ANY($6::date[])
			GROUP BY ul.user_id
		), tiers AS (
			SELECT min_tokens, rate_multiplier FROM jsonb_to_recordset($4::jsonb) AS t(min_tokens bigint, rate_multiplier numeric)
		)
		INSERT INTO downstream_auto_user_group_rates (user_id, group_id, rate_multiplier)
		SELECT m.user_id, m.group_id, chosen.rate_multiplier FROM members m
		JOIN usage u ON u.user_id = m.user_id
		JOIN LATERAL (SELECT rate_multiplier FROM tiers WHERE min_tokens <= u.tokens::numeric / $7::numeric ORDER BY min_tokens DESC LIMIT 1) chosen ON TRUE`,
		pq.Array(groupIDs), start, end, tiersJSON, endDate.Location().String(), pq.Array(dateStrings), len(workingDays))
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE downstream_auto_rate_config SET last_run_date = $1 WHERE id = TRUE`, endDate.Format("2006-01-02")); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
