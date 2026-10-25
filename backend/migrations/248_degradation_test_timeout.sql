-- Existing generation plans receive a ten-minute execution budget.
ALTER TABLE degradation_test_plans
    ADD COLUMN IF NOT EXISTS timeout_seconds INTEGER NOT NULL DEFAULT 600
    CHECK (timeout_seconds BETWEEN 60 AND 1800);
