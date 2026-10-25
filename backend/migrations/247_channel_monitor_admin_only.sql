-- Existing monitors remain visible; administrators can explicitly restrict one.
ALTER TABLE channel_monitors
    ADD COLUMN IF NOT EXISTS admin_only BOOLEAN NOT NULL DEFAULT false;
