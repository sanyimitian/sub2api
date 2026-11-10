-- 仅关联展示名称，不参与检测请求的路由、密钥分配或调度。
ALTER TABLE model_identification_targets
    ADD COLUMN display_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL;
CREATE INDEX idx_model_identification_display_group
    ON model_identification_targets(display_group_id) WHERE display_group_id IS NOT NULL;
COMMENT ON COLUMN model_identification_targets.display_group_id IS '管理员选择的站内展示分组，不决定实际请求使用哪个分组';
-- 旧配置不按名称或数字 ID 猜测关联，管理员编辑时自行选择。
