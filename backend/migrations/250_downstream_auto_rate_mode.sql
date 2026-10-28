ALTER TABLE downstream_auto_rate_config
    ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'weekly'
    CHECK (mode IN ('daily', 'weekly'));
