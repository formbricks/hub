-- Add event_types column to webhooks table for event type filtering
-- Run with: psql $DATABASE_URL -f sql/003_webhook_event_types.sql
-- Safe to run multiple times (idempotent)

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'webhooks' AND column_name = 'event_types'
  ) THEN
    ALTER TABLE webhooks ADD COLUMN event_types VARCHAR(64)[];
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_webhooks_event_types ON webhooks USING GIN (event_types);
