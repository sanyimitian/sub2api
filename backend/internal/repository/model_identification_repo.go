package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type modelIdentificationRepository struct{ db *sql.DB }

func NewModelIdentificationRepository(db *sql.DB) service.ModelIdentificationRepository {
	return &modelIdentificationRepository{db: db}
}
func (r *modelIdentificationRepository) ListGroups(ctx context.Context) ([]*service.ModelIdentificationGroup, error) {
	rows, err := r.db.QueryContext(ctx, `
 SELECT p.id,p.group_id,p.model_id,p.cron_expression,p.enabled,
 p.last_run_at,p.next_run_at,p.created_at,p.updated_at,p.prompt,p.running_until,p.reasoning_effort,p.timeout_seconds,
 COALESCE(sg.name,g.name),g.protocol,sg.id,
 COALESCE((SELECT MAX(id) FROM degradation_test_results WHERE plan_id=p.id),0),
 (SELECT COUNT(*) FROM degradation_test_results WHERE plan_id=p.id)
 FROM degradation_test_plans p JOIN model_identification_targets g ON g.id=p.group_id
 LEFT JOIN groups sg ON sg.id=g.display_group_id AND sg.deleted_at IS NULL
 ORDER BY COALESCE(sg.name,g.name),g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]*service.ModelIdentificationGroup, 0)
	for rows.Next() {
		plan := &service.DegradationTestPlan{MaxResults: service.DegradationTestMaxResults}
		group := &service.ModelIdentificationGroup{Plan: plan}
		if err := rows.Scan(&plan.ID, &group.GroupID, &plan.ModelID, &plan.CronExpression, &plan.Enabled, &plan.LastRunAt, &plan.NextRunAt, &plan.CreatedAt, &plan.UpdatedAt, &plan.Prompt, &plan.RunningUntil, &plan.ReasoningEffort, &plan.TimeoutSeconds, &group.Name, &group.Platform, &group.DisplayGroupID, &group.LatestResultID, &group.ResultCount); err != nil {
			return nil, err
		}
		plan.GroupID = &group.GroupID
		groups = append(groups, group)
	}
	return groups, rows.Err()
}
func (r *modelIdentificationRepository) GetTarget(ctx context.Context, id int64) (*service.ModelIdentificationTarget, error) {
	target := &service.ModelIdentificationTarget{}
	err := r.db.QueryRowContext(ctx, `SELECT t.id,COALESCE(g.name,t.name),t.endpoint,t.encrypted_api_key,t.protocol,g.id FROM model_identification_targets t LEFT JOIN groups g ON g.id=t.display_group_id AND g.deleted_at IS NULL WHERE t.id=$1`, id).Scan(&target.ID, &target.Name, &target.Endpoint, &target.EncryptedAPIKey, &target.Protocol, &target.DisplayGroupID)
	return target, err
}
func (r *modelIdentificationRepository) Save(ctx context.Context, target *service.ModelIdentificationTarget, plan *service.DegradationTestPlan) (*service.DegradationTestPlan, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// 在同一事务验证关联并保存当时的真实名称；分组删除后可用该名称展示历史。
	if target.DisplayGroupID != nil {
		err = tx.QueryRowContext(ctx, `SELECT name FROM groups WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, *target.DisplayGroupID).Scan(&target.Name)
		if err == sql.ErrNoRows {
			return nil, service.ErrGroupNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	if plan.ID == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO model_identification_targets(name,endpoint,encrypted_api_key,protocol,display_group_id) VALUES($1,$2,$3,$4,$5) RETURNING id`, target.Name, target.Endpoint, target.EncryptedAPIKey, target.Protocol, target.DisplayGroupID).Scan(&target.ID)
	} else {
		// 不允许将私有账号计划变为共享鉴定。
		err = tx.QueryRowContext(ctx, `UPDATE model_identification_targets SET name=$2,endpoint=$3,encrypted_api_key=$4,protocol=$5,display_group_id=$7 WHERE id=$1 AND EXISTS(SELECT 1 FROM degradation_test_plans WHERE id=$6 AND group_id=$1) RETURNING id`, target.ID, target.Name, target.Endpoint, target.EncryptedAPIKey, target.Protocol, plan.ID, target.DisplayGroupID).Scan(&target.ID)
	}
	if err != nil {
		return nil, err
	}
	plan.GroupID = &target.ID
	var saved *service.DegradationTestPlan
	if plan.ID == 0 {
		saved, err = createDegradationPlan(ctx, tx, plan)
	} else {
		saved, err = updateDegradationPlan(ctx, tx, plan)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}
func (r *modelIdentificationRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM model_identification_targets WHERE id=(SELECT group_id FROM degradation_test_plans WHERE id=$1)`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
