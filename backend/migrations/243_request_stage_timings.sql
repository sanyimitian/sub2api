ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS request_timings JSONB;

ALTER TABLE ops_error_logs
    ADD COLUMN IF NOT EXISTS request_timings JSONB;
