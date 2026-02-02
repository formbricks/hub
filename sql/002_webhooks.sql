-- Webhooks table for Standard Webhooks implementation
-- Run with: psql $DATABASE_URL -f sql/002_webhooks.sql
-- Safe to run multiple times (idempotent)

CREATE TABLE IF NOT EXISTS webhooks (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  url VARCHAR(2048) NOT NULL,
  signing_key VARCHAR(255) NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  tenant_id VARCHAR(255),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_webhooks_enabled ON webhooks(enabled);
CREATE INDEX IF NOT EXISTS idx_webhooks_tenant_id ON webhooks(tenant_id);

-- Add columns to existing table (idempotent)
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'webhooks' AND column_name = 'enabled') THEN
    ALTER TABLE webhooks ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;
  END IF;
END $$;
