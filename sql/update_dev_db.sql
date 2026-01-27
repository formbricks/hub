-- Update script for development databases
-- Run with: psql $DATABASE_URL -f sql/update_dev_db.sql
-- Safe to run multiple times (idempotent)

-- Add connector_instances table
CREATE TABLE IF NOT EXISTS connector_instances (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  name VARCHAR(255) NOT NULL,
  instance_id VARCHAR(255) NOT NULL,
  type VARCHAR(50) NOT NULL CHECK (type IN ('polling', 'webhook', 'output', 'enrichment')),
  config JSONB NOT NULL DEFAULT '{}',
  state JSONB DEFAULT '{}',
  running BOOLEAN NOT NULL DEFAULT false,
  error VARCHAR,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  UNIQUE(name, instance_id)
);

-- Add columns to existing table (idempotent)
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'connector_instances' AND column_name = 'running') THEN
    ALTER TABLE connector_instances ADD COLUMN running BOOLEAN NOT NULL DEFAULT false;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'connector_instances' AND column_name = 'error') THEN
    ALTER TABLE connector_instances ADD COLUMN error VARCHAR;
  END IF;
END $$;

-- Optimized indexes (with IF NOT EXISTS checks)
CREATE INDEX IF NOT EXISTS idx_connector_instances_type_running_created
  ON connector_instances(type, running, created_at);

CREATE INDEX IF NOT EXISTS idx_connector_instances_type_running_partial
  ON connector_instances(type, running) WHERE running = true;

CREATE INDEX IF NOT EXISTS idx_connector_instances_name_instance_id
  ON connector_instances(name, instance_id);
