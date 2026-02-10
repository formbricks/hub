-- +goose Up
-- Normalize timestamp columns to TIMESTAMPTZ (feedback_records from 001)
ALTER TABLE feedback_records
  ALTER COLUMN collected_at TYPE TIMESTAMPTZ USING collected_at AT TIME ZONE 'UTC',
  ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC',
  ALTER COLUMN updated_at TYPE TIMESTAMPTZ USING updated_at AT TIME ZONE 'UTC',
  ALTER COLUMN value_date TYPE TIMESTAMPTZ USING value_date AT TIME ZONE 'UTC';

-- Webhooks table for Standard Webhooks implementation
CREATE TABLE webhooks (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  url VARCHAR(2048) NOT NULL,
  signing_key VARCHAR(255) NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  tenant_id VARCHAR(255),
  event_types VARCHAR(64)[],
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  disabled_reason TEXT,
  disabled_at TIMESTAMPTZ
);

CREATE INDEX idx_webhooks_enabled ON webhooks(enabled);
CREATE INDEX idx_webhooks_tenant_id ON webhooks(tenant_id);
CREATE INDEX idx_webhooks_event_types ON webhooks USING GIN (event_types);
CREATE INDEX idx_webhooks_enabled_created_at ON webhooks(enabled, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS webhooks;

ALTER TABLE feedback_records
  ALTER COLUMN collected_at TYPE TIMESTAMP USING collected_at AT TIME ZONE 'UTC',
  ALTER COLUMN created_at TYPE TIMESTAMP USING created_at AT TIME ZONE 'UTC',
  ALTER COLUMN updated_at TYPE TIMESTAMP USING updated_at AT TIME ZONE 'UTC',
  ALTER COLUMN value_date TYPE TIMESTAMP USING value_date AT TIME ZONE 'UTC';
