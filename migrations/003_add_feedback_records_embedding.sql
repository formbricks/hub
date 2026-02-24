-- +goose Up
-- Embeddings table: one row per feedback_record per model (e.g. openai text-embedding-3-small).
-- Requires vector extension from 001_initial_schema.sql.
-- ON DELETE CASCADE: embeddings are deleted when the referenced feedback record is deleted.
CREATE TABLE embeddings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  feedback_record_id UUID NOT NULL REFERENCES feedback_records(id) ON DELETE CASCADE,
  embedding vector NOT NULL,
  model TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (feedback_record_id, model)
);

-- +goose Down
DROP TABLE IF EXISTS embeddings;
