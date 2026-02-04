-- +goose Up
-- Add disabled_reason and disabled_at for webhooks disabled after delivery failure (410 or max retries)

ALTER TABLE webhooks ADD COLUMN IF NOT EXISTS disabled_reason TEXT;
ALTER TABLE webhooks ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE webhooks DROP COLUMN IF EXISTS disabled_at;
ALTER TABLE webhooks DROP COLUMN IF EXISTS disabled_reason;
