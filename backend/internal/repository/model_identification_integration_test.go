//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelIdentificationGroupLifecyclePostgres(t *testing.T) {
	ctx := context.Background()
	group := &service.ModelIdentificationTarget{Name: "independent-identification", Endpoint: "https://example.com/v1", EncryptedAPIKey: "encrypted-secret", Protocol: "responses"}
	plans := NewDegradationTestPlanRepository(integrationDB)
	records := NewDegradationTestResultRepository(integrationDB)
	shared := NewModelIdentificationRepository(integrationDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Minute)
	plan, err := shared.Save(ctx, group, &service.DegradationTestPlan{GroupID: &group.ID, ModelID: "test-model", Prompt: service.DefaultDegradationTestPrompt, CronExpression: "@every 30m", Enabled: true, NextRunAt: &due})
	require.NoError(t, err)
	require.Zero(t, plan.AccountID)
	require.Equal(t, group.ID, *plan.GroupID)
	t.Cleanup(func() { _ = shared.Delete(ctx, plan.ID) })
	_, err = plans.Create(ctx, &service.DegradationTestPlan{GroupID: &group.ID, ModelID: "duplicate", Prompt: "prompt", CronExpression: "@every 30m", Enabled: true})
	require.Error(t, err, "each group has one plan")
	duePlans, err := plans.ListDue(ctx, now)
	require.NoError(t, err)
	found := false
	for _, p := range duePlans {
		if p.ID == plan.ID {
			found = true
			require.Equal(t, group.ID, *p.GroupID)
		}
	}
	require.True(t, found)
	claimed, err := plans.ClaimRun(ctx, plan.ID, true, now)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	second, err := plans.ClaimRun(ctx, plan.ID, false, now)
	require.NoError(t, err)
	require.Nil(t, second)
	plan.ModelID = "updated-model"
	plan.CronExpression = "@every 90m"
	next := now.Add(90 * time.Minute)
	plan.NextRunAt = &next
	updated, err := shared.Save(ctx, group, plan)
	require.NoError(t, err)
	require.Equal(t, group.ID, *updated.GroupID)
	oldNext := now.Add(30 * time.Minute)
	require.NoError(t, plans.CompleteRun(ctx, claimed, now, &oldNext))
	latest, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.WithinDuration(t, next, *latest.NextRunAt, time.Millisecond)
	for i := 0; i < 26; i++ {
		status := "success"
		if i%2 == 1 {
			status = "failed"
		}
		_, err = records.Create(ctx, &service.DegradationTestResult{PlanID: plan.ID, ModelID: "test-model", Prompt: "prompt", Status: status, ResponseText: "<svg></svg>", StartedAt: now, FinishedAt: now})
		require.NoError(t, err)
	}
	require.NoError(t, records.PruneOldResults(ctx, plan.ID, 24))
	rows, err := records.ListByPlanID(ctx, plan.ID, 24)
	require.NoError(t, err)
	require.Len(t, rows, 24)
	groups, err := shared.ListGroups(ctx)
	require.NoError(t, err)
	var own *service.ModelIdentificationGroup
	for _, g := range groups {
		if g.GroupID == group.ID {
			own = g
		}
	}
	require.NotNil(t, own)
	require.Equal(t, 24, own.ResultCount)
	require.Equal(t, rows[0].ID, own.LatestResultID)
	_, err = integrationDB.ExecContext(ctx, "UPDATE degradation_test_results SET created_at = NOW() - INTERVAL '8 days' WHERE id=$1", rows[0].ID)
	require.NoError(t, err)
	rows, err = records.ListByPlanID(ctx, plan.ID, 24)
	require.NoError(t, err)
	require.Len(t, rows, 24, "long intervals must still retain the latest 24 runs")
	groups, err = shared.ListGroups(ctx)
	require.NoError(t, err)
	for _, g := range groups {
		if g.GroupID == group.ID {
			require.Equal(t, 24, g.ResultCount)
		}
	}
	encoded, err := json.Marshal(groups)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), group.Endpoint)
	require.NotContains(t, string(encoded), group.EncryptedAPIKey)
	target, err := shared.GetTarget(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, group.EncryptedAPIKey, target.EncryptedAPIKey)
	require.NoError(t, shared.Delete(ctx, plan.ID))
	_, err = plans.GetByID(ctx, plan.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = shared.GetTarget(ctx, group.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	duePlans, err = plans.ListDue(ctx, now.Add(time.Hour*24))
	require.NoError(t, err)
	for _, p := range duePlans {
		require.NotEqual(t, plan.ID, p.ID)
	}
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM degradation_test_results WHERE plan_id=$1", plan.ID).Scan(&count))
	require.Zero(t, count)
}

func TestModelIdentificationSaveRollsBackTargetWhenPlanFails(t *testing.T) {
	ctx := context.Background()
	shared := NewModelIdentificationRepository(integrationDB)
	target := &service.ModelIdentificationTarget{Name: "rollback-target", Endpoint: "https://example.com", EncryptedAPIKey: "encrypted", Protocol: "responses"}
	_, err := shared.Save(ctx, target, &service.DegradationTestPlan{ModelID: strings.Repeat("x", 101), Prompt: "prompt", CronExpression: "@every 30m"})
	require.Error(t, err)
	_, err = shared.GetTarget(ctx, target.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// 在事务中重建已应用 252 的旧状态，验证升级保留作品、停用缺少凭证的计划。
func TestModelIdentificationCredentialMigrationPreservesHistory(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	_, err := tx.ExecContext(ctx, `ALTER TABLE degradation_test_plans DROP CONSTRAINT degradation_test_plans_group_id_fkey;
 DROP TABLE model_identification_targets;
 ALTER TABLE degradation_test_plans ADD CONSTRAINT degradation_test_plans_group_id_fkey FOREIGN KEY(group_id) REFERENCES groups(id) ON DELETE CASCADE`)
	require.NoError(t, err)
	var groupID, planID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups(name,platform,rate_multiplier,status) VALUES('legacy-identification','anthropic',1,'active') RETURNING id`).Scan(&groupID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO degradation_test_plans(group_id,model_id,prompt,cron_expression,enabled) VALUES($1,'alias','prompt','@every 30m',true) RETURNING id`, groupID).Scan(&planID))
	_, err = tx.ExecContext(ctx, `INSERT INTO degradation_test_results(plan_id,model_id,prompt,status,response_text,started_at,finished_at) VALUES($1,'alias','prompt','success','legacy-artwork',NOW(),NOW())`, planID)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("253_model_identification_credentials.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var name, endpoint, cipher, protocol string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT name,endpoint,encrypted_api_key,protocol FROM model_identification_targets WHERE id=$1`, groupID).Scan(&name, &endpoint, &cipher, &protocol))
	require.Equal(t, "legacy-identification", name)
	require.Empty(t, endpoint)
	require.Empty(t, cipher)
	require.Equal(t, "anthropic", protocol)
	var enabled bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT enabled FROM degradation_test_plans WHERE id=$1`, planID).Scan(&enabled))
	require.False(t, enabled)

	// 新增展示关联不按旧目标编号推断站内分组。
	displayMigration, err := dbmigrations.FS.ReadFile("254_model_identification_display_group.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(displayMigration))
	require.NoError(t, err)
	var displayGroupID sql.NullInt64
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT display_group_id FROM model_identification_targets WHERE id=$1", groupID).Scan(&displayGroupID))
	require.False(t, displayGroupID.Valid)
	// 删除原站内分组不再影响独立检测配置和作品。
	_, err = tx.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, groupID)
	require.NoError(t, err)
	var content string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT response_text FROM degradation_test_results WHERE plan_id=$1`, planID).Scan(&content))
	require.Equal(t, "legacy-artwork", content)
	var nextID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO model_identification_targets(name) VALUES('new-target') RETURNING id`).Scan(&nextID))
	require.Greater(t, nextID, groupID)
}

// 保存事务已修改目标但尚未更新计划时，抢占必须取得完整旧版；提交后取得完整新版。
func TestModelIdentificationClaimCapturesConsistentConfiguration(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		t.Run(fmt.Sprint(scheduled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			repo := NewModelIdentificationRepository(integrationDB)
			plans := NewDegradationTestPlanRepository(integrationDB)
			target := &service.ModelIdentificationTarget{Name: "snapshot", Endpoint: "https://old.example.com", EncryptedAPIKey: "old-cipher", Protocol: "chat_completions"}
			now := time.Now().UTC().Truncate(time.Microsecond)
			due := now.Add(-time.Minute)
			plan, err := repo.Save(ctx, target, &service.DegradationTestPlan{ModelID: "old-model", ReasoningEffort: "low", Prompt: "old-prompt", TimeoutSeconds: 60, CronExpression: "@every 30m", Enabled: true, NextRunAt: &due})
			require.NoError(t, err)
			defer repo.Delete(context.Background(), plan.ID)
			edit, err := integrationDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer edit.Rollback()
			_, err = edit.ExecContext(ctx, `UPDATE model_identification_targets SET endpoint='https://new.example.com',encrypted_api_key='new-cipher',protocol='responses' WHERE id=$1`, target.ID)
			require.NoError(t, err)
			claimed, err := plans.ClaimRun(ctx, plan.ID, scheduled, now)
			require.NoError(t, err)
			require.NotNil(t, claimed)
			require.NotNil(t, claimed.IdentificationTarget)
			require.Equal(t, "old-model", claimed.ModelID)
			require.Equal(t, "low", claimed.ReasoningEffort)
			require.Equal(t, 60, claimed.TimeoutSeconds)
			require.Equal(t, "https://old.example.com", claimed.IdentificationTarget.Endpoint)
			require.Equal(t, "old-cipher", claimed.IdentificationTarget.EncryptedAPIKey)
			require.Equal(t, "chat_completions", claimed.IdentificationTarget.Protocol)
			// 按与实际保存相同的顺序更新计划后提交。
			plan.ModelID = "new-model"
			plan.ReasoningEffort = "high"
			plan.Prompt = "new-prompt"
			plan.TimeoutSeconds = 900
			_, err = updateDegradationPlan(ctx, edit, plan)
			require.NoError(t, err)
			require.NoError(t, edit.Commit())
			require.Equal(t, "old-model", claimed.ModelID)
			require.Equal(t, "old-cipher", claimed.IdentificationTarget.EncryptedAPIKey)
			encoded, err := json.Marshal(claimed)
			require.NoError(t, err)
			for _, secret := range []string{"old.example.com", "old-cipher", "IdentificationTarget", "encrypted_api_key", "endpoint"} {
				require.NotContains(t, string(encoded), secret)
			}
			require.NoError(t, plans.CompleteRun(ctx, claimed, now, nil))
			next, err := plans.ClaimRun(ctx, plan.ID, scheduled, now.Add(time.Second))
			require.NoError(t, err)
			require.NotNil(t, next)
			require.NotNil(t, next.IdentificationTarget)
			require.Equal(t, "new-model", next.ModelID)
			require.Equal(t, "high", next.ReasoningEffort)
			require.Equal(t, 900, next.TimeoutSeconds)
			require.Equal(t, "https://new.example.com", next.IdentificationTarget.Endpoint)
			require.Equal(t, "new-cipher", next.IdentificationTarget.EncryptedAPIKey)
			require.Equal(t, "responses", next.IdentificationTarget.Protocol)
		})
	}
}

// 展示关联不改变目标标识、调用凭证、执行计划或作品归属。
func TestModelIdentificationDisplayGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := NewModelIdentificationRepository(integrationDB)
	plans := NewDegradationTestPlanRepository(integrationDB)
	records := NewDegradationTestResultRepository(integrationDB)
	var first, second int64
	for _, item := range []struct {
		id           *int64
		name, status string
	}{
		{&first, "展示分组甲", "active"}, {&second, "展示分组乙", "inactive"},
	} {
		require.NoError(t, integrationDB.QueryRowContext(ctx, "INSERT INTO groups(name,platform,rate_multiplier,status) VALUES($1,'anthropic',1,$2) RETURNING id", item.name, item.status).Scan(item.id))
	}
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1,$2)", first, second) })
	target := &service.ModelIdentificationTarget{DisplayGroupID: &first, Name: "不能使用输入的名称", Endpoint: "https://example.com/v1", EncryptedAPIKey: "independent-cipher", Protocol: "responses"}
	plan, err := repo.Save(ctx, target, &service.DegradationTestPlan{ModelID: "independent-model", Prompt: "prompt", CronExpression: "@every 30m", Enabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = repo.Delete(ctx, plan.ID) })
	targetID := target.ID
	now := time.Now().UTC()
	result, err := records.Create(ctx, &service.DegradationTestResult{PlanID: plan.ID, ModelID: plan.ModelID, Prompt: "prompt", Status: "success", ResponseText: "<svg></svg>", StartedAt: now, FinishedAt: now})
	require.NoError(t, err)
	check := func(name string, association *int64) {
		t.Helper()
		current, err := repo.GetTarget(ctx, targetID)
		require.NoError(t, err)
		require.Equal(t, name, current.Name)
		require.Equal(t, association, current.DisplayGroupID)
		require.Equal(t, target.Endpoint, current.Endpoint)
		require.Equal(t, target.EncryptedAPIKey, current.EncryptedAPIKey)
		groups, err := repo.ListGroups(ctx)
		require.NoError(t, err)
		found := false
		for _, group := range groups {
			if group.GroupID != targetID {
				continue
			}
			found = true
			require.Equal(t, name, group.Name)
			require.Equal(t, association, group.DisplayGroupID)
			require.Equal(t, plan.ID, group.Plan.ID)
			require.Equal(t, plan.ModelID, group.Plan.ModelID)
			require.True(t, group.Plan.Enabled)
			require.Equal(t, result.ID, group.LatestResultID)
			require.Equal(t, 1, group.ResultCount)
		}
		require.True(t, found)
	}
	check("展示分组甲", &first)
	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET name='展示分组甲改名' WHERE id=$1", first)
	require.NoError(t, err)
	check("展示分组甲改名", &first)
	// 可以关联停用分组；只修改标签，不改变调用配置和历史归属。
	target.DisplayGroupID = &second
	plan, err = repo.Save(ctx, target, plan)
	require.NoError(t, err)
	require.Equal(t, targetID, *plan.GroupID)
	check("展示分组乙", &second)
	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET deleted_at=NOW() WHERE id=$1", second)
	require.NoError(t, err)
	check("展示分组乙", nil)
	_, err = repo.Save(ctx, target, plan)
	require.ErrorIs(t, err, service.ErrGroupNotFound)
	_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id=$1", second)
	require.NoError(t, err)
	check("展示分组乙", nil)
	claimed, err := plans.ClaimRun(ctx, plan.ID, false, now)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, target.Endpoint, claimed.IdentificationTarget.Endpoint)
	require.Equal(t, target.EncryptedAPIKey, claimed.IdentificationTarget.EncryptedAPIKey)
	require.Equal(t, plan.ModelID, claimed.ModelID)
	// 不存在的关联必须在创建目标之前失败。
	missing := int64(-1)
	target.DisplayGroupID = &missing
	_, err = repo.Save(ctx, target, &service.DegradationTestPlan{ModelID: "model", Prompt: "prompt", CronExpression: "@every 30m"})
	require.ErrorIs(t, err, service.ErrGroupNotFound)
	check("展示分组乙", nil)
}
