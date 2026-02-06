-- +goose Up
-- Webhooks table for Standard Webhooks implementation

CREATE TABLE webhooks (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  url VARCHAR(2048) NOT NULL,
  signing_key VARCHAR(255) NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  tenant_id VARCHAR(255),
  event_types VARCHAR(64)[],
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhooks_enabled ON webhooks(enabled);
CREATE INDEX idx_webhooks_tenant_id ON webhooks(tenant_id);
CREATE INDEX idx_webhooks_event_types ON webhooks USING GIN (event_types);

-- +goose Down
DROP TABLE IF EXISTS webhooks;
