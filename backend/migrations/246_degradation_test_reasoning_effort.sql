-- Keep an explicit requested effort on both plans and execution snapshots.
-- Empty preserves the upstream default for all existing plans/results.
ALTER TABLE degradation_test_plans
    ADD COLUMN IF NOT EXISTS reasoning_effort VARCHAR(16) NOT NULL DEFAULT ''
    CHECK (reasoning_effort IN ('', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh'));

ALTER TABLE degradation_test_results
    ADD COLUMN IF NOT EXISTS reasoning_effort VARCHAR(16) NOT NULL DEFAULT ''
    CHECK (reasoning_effort IN ('', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh'));
