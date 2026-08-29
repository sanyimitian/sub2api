-- Persist the explicit "use current service" monitor switch.
ALTER TABLE channel_monitors
    ADD COLUMN IF NOT EXISTS use_current_service BOOLEAN NOT NULL DEFAULT FALSE;
