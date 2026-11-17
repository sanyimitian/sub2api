-- Empty lists preserve the existing global default group behavior.
ALTER TABLE ops_alert_rules
    ADD COLUMN IF NOT EXISTS qq_group_openids TEXT[] NOT NULL DEFAULT '{}';
