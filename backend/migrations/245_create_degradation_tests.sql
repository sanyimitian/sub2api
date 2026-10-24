-- Prompt generation tests are independent of the existing scheduled health checks.
CREATE TABLE IF NOT EXISTS degradation_test_plans (
    id              BIGSERIAL PRIMARY KEY,
    account_id      BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model_id        VARCHAR(100) NOT NULL,
    prompt          TEXT NOT NULL,
    cron_expression VARCHAR(100) NOT NULL DEFAULT '@every 30m',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    max_results     INT NOT NULL DEFAULT 24 CHECK (max_results = 24),
    last_run_at     TIMESTAMPTZ,
    next_run_at     TIMESTAMPTZ,
    running_until   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_dtp_account_id ON degradation_test_plans(account_id);
CREATE INDEX IF NOT EXISTS idx_dtp_enabled_next_run ON degradation_test_plans(enabled, next_run_at) WHERE enabled = true;

CREATE TABLE IF NOT EXISTS degradation_test_results (
    id            BIGSERIAL PRIMARY KEY,
    plan_id       BIGINT NOT NULL REFERENCES degradation_test_plans(id) ON DELETE CASCADE,
    model_id      VARCHAR(100) NOT NULL,
    prompt        TEXT NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'success',
    response_text TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    latency_ms    BIGINT NOT NULL DEFAULT 0,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_dtr_plan_created ON degradation_test_results(plan_id, created_at DESC, id DESC);
