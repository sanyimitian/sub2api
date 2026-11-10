-- 账号测试继续保持私有；分组测试为全站共享的模型鉴定提供独立计划。
ALTER TABLE degradation_test_plans ALTER COLUMN account_id DROP NOT NULL;
ALTER TABLE degradation_test_plans ADD COLUMN group_id BIGINT REFERENCES groups(id) ON DELETE CASCADE;
ALTER TABLE degradation_test_plans ADD CONSTRAINT degradation_test_target_check
    CHECK ((account_id IS NOT NULL AND group_id IS NULL) OR (account_id IS NULL AND group_id IS NOT NULL));
CREATE UNIQUE INDEX idx_degradation_test_group ON degradation_test_plans(group_id) WHERE group_id IS NOT NULL;
