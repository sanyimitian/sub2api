CREATE TABLE IF NOT EXISTS downstream_auto_rate_config (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    group_ids BIGINT[] NOT NULL DEFAULT '{}',
    tiers JSONB NOT NULL DEFAULT '[]',
    last_run_date DATE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO downstream_auto_rate_config (id, tiers) VALUES
    (TRUE, '[{"min_tokens":500000000,"rate_multiplier":0.17},{"min_tokens":1000000000,"rate_multiplier":0.155},{"min_tokens":2000000000,"rate_multiplier":0.15},{"min_tokens":5000000000,"rate_multiplier":0.145},{"min_tokens":30000000000,"rate_multiplier":0.14}]')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS downstream_auto_user_group_rates (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    rate_multiplier DECIMAL(10,4) NOT NULL CHECK (rate_multiplier > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_downstream_auto_user_group_rates_group_id
    ON downstream_auto_user_group_rates (group_id);
