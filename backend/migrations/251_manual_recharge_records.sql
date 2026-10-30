CREATE TABLE IF NOT EXISTS manual_recharge_records (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    user_email TEXT NOT NULL,
    amount DECIMAL(20, 8) NOT NULL CHECK (amount > 0),
    platform VARCHAR(16) NOT NULL CHECK (platform IN ('wechat', 'alipay', 'other')),
    recipient VARCHAR(8) NOT NULL CHECK (recipient IN ('鹏', '杰', '翔')),
    notes TEXT NOT NULL DEFAULT '',
    redeem_code_id BIGINT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT manual_recharge_other_notes CHECK (platform <> 'other' OR length(btrim(notes)) > 0)
);

CREATE INDEX IF NOT EXISTS idx_manual_recharge_records_created_at
    ON manual_recharge_records (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_manual_recharge_records_user_id
    ON manual_recharge_records (user_id);

CREATE TABLE IF NOT EXISTS manual_recharge_accounts (
    recipient VARCHAR(8) PRIMARY KEY CHECK (recipient IN ('鹏', '杰', '翔')),
    balance DECIMAL(20, 8) NOT NULL DEFAULT 0 CHECK (balance >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS manual_recharge_deductions (
    id BIGSERIAL PRIMARY KEY,
    recipient VARCHAR(8) NOT NULL REFERENCES manual_recharge_accounts (recipient),
    amount DECIMAL(20, 8) NOT NULL CHECK (amount > 0),
    notes TEXT NOT NULL CHECK (length(btrim(notes)) > 0),
    screenshot BYTEA,
    screenshot_mime VARCHAR(32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT manual_recharge_deduction_screenshot_mime CHECK (screenshot IS NULL OR screenshot_mime IN ('image/png', 'image/jpeg', 'image/webp'))
);

CREATE INDEX IF NOT EXISTS idx_manual_recharge_deductions_created_at
    ON manual_recharge_deductions (created_at DESC, id DESC);
