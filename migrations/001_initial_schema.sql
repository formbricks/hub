-- Initial schema for Formbricks Hub

-- Enable extensions
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Feedback records table
CREATE TABLE feedback_records (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  collected_at TIMESTAMP NOT NULL DEFAULT NOW(),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  source_type VARCHAR NOT NULL,
  source_id VARCHAR,
  source_name VARCHAR,

  field_id VARCHAR NOT NULL,
  field_label VARCHAR,
  field_type VARCHAR NOT NULL,

  value_text TEXT,
  value_number DOUBLE PRECISION,
  value_boolean BOOLEAN,
  value_date TIMESTAMP,
  value_json JSONB,

  metadata JSONB,
  language VARCHAR(10),
  user_identifier VARCHAR,

  -- Multi-tenancy fields
  tenant_id VARCHAR(255),
  response_id VARCHAR(255)
);

-- API keys table
CREATE TABLE api_keys (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  key_hash VARCHAR(255) NOT NULL UNIQUE,
  name VARCHAR(255),
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  last_used_at TIMESTAMP
);

-- Indexes
CREATE INDEX idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX idx_feedback_records_tenant_id ON feedback_records(tenant_id);
CREATE INDEX idx_feedback_records_response_id ON feedback_records(response_id);
