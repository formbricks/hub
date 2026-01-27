-- Initial schema for Formbricks Hub

-- Enable extensions
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Feedback records table
CREATE TABLE feedback_records (
  id UUID PRIMARY KEY DEFAULT uuidv7(),

  collected_at TIMESTAMP NOT NULL DEFAULT NOW(),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  source_type VARCHAR NOT NULL,
  source_id VARCHAR(255),
  source_name VARCHAR,

  field_id VARCHAR(255) NOT NULL,
  field_label VARCHAR,
  field_type VARCHAR NOT NULL,

  value_text TEXT,
  value_number DOUBLE PRECISION,
  value_boolean BOOLEAN,
  value_date TIMESTAMP,

  metadata JSONB,
  language VARCHAR(10),
  user_identifier VARCHAR(255),

  -- Multi-tenancy fields
  tenant_id VARCHAR(255),
  response_id VARCHAR(255)
);

-- Indexes
-- Multi-tenancy indexes
CREATE INDEX idx_feedback_records_tenant_id ON feedback_records(tenant_id);
CREATE INDEX idx_feedback_records_response_id ON feedback_records(response_id);

-- Single-column indexes for common filter operations
-- Required for analytics performance
CREATE INDEX idx_feedback_records_source_type ON feedback_records(source_type);
CREATE INDEX idx_feedback_records_source_id ON feedback_records(source_id);
CREATE INDEX idx_feedback_records_collected_at ON feedback_records(collected_at);
CREATE INDEX idx_feedback_records_field_type ON feedback_records(field_type);
CREATE INDEX idx_feedback_records_field_id ON feedback_records(field_id);
CREATE INDEX idx_feedback_records_value_number ON feedback_records(value_number);
CREATE INDEX idx_feedback_records_user_identifier ON feedback_records(user_identifier);

-- Composite indexes for common query patterns with tenant_id
-- These optimize queries that filter by tenant_id first (common in Formbricks Cloud)
-- and then apply additional filters
CREATE INDEX idx_feedback_records_tenant_user_identifier ON feedback_records(tenant_id, user_identifier);
CREATE INDEX idx_feedback_records_tenant_collected_at ON feedback_records(tenant_id, collected_at);
CREATE INDEX idx_feedback_records_tenant_source_type ON feedback_records(tenant_id, source_type);
CREATE INDEX idx_feedback_records_tenant_field_type ON feedback_records(tenant_id, field_type);

-- Connector instances table
CREATE TABLE connector_instances (
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

-- Optimized indexes for connector instances
-- Composite index for ListRunningByType query (sorted by created_at)
CREATE INDEX idx_connector_instances_type_running_created ON connector_instances(type, running, created_at);

-- Partial index for CountRunningByType query (only indexes running instances)
CREATE INDEX idx_connector_instances_type_running_partial ON connector_instances(type, running) WHERE running = true;

-- Lookup index for GetByNameAndInstanceID (covered by unique constraint but explicit for query planner)
CREATE INDEX idx_connector_instances_name_instance_id ON connector_instances(name, instance_id);
