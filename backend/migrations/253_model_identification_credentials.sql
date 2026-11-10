-- 检测分组是独立的地址/密钥配置，不再关联站内账号分组。
CREATE TABLE model_identification_targets (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    endpoint TEXT NOT NULL DEFAULT '',
    encrypted_api_key TEXT NOT NULL DEFAULT '',
    protocol VARCHAR(32) NOT NULL DEFAULT 'chat_completions'
        CHECK (protocol IN ('chat_completions', 'responses', 'anthropic', 'gemini'))
);

-- 保留旧版配置和作品；缺少真实地址/密钥的计划暂停，等待管理员补齐。
INSERT INTO model_identification_targets (id, name, protocol)
SELECT g.id, g.name, CASE g.platform
    WHEN 'anthropic' THEN 'anthropic' WHEN 'gemini' THEN 'gemini'
    ELSE 'chat_completions' END
FROM groups g WHERE EXISTS (SELECT 1 FROM degradation_test_plans p WHERE p.group_id = g.id);
SELECT setval(pg_get_serial_sequence('model_identification_targets', 'id'),
    COALESCE((SELECT MAX(id) FROM model_identification_targets), 1),
    EXISTS (SELECT 1 FROM model_identification_targets));
UPDATE degradation_test_plans SET enabled = FALSE WHERE group_id IS NOT NULL;
ALTER TABLE degradation_test_plans DROP CONSTRAINT degradation_test_plans_group_id_fkey;
ALTER TABLE degradation_test_plans ADD CONSTRAINT degradation_test_plans_group_id_fkey
    FOREIGN KEY (group_id) REFERENCES model_identification_targets(id) ON DELETE CASCADE;
COMMENT ON COLUMN degradation_test_plans.group_id IS '独立模型鉴定配置 ID，与站内 groups 无关';
