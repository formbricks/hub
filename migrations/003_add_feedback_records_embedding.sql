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

-- HNSW index for cosine similarity search (OpenAI text-embedding-3-small).
CREATE INDEX idx_embeddings_openai ON embeddings USING hnsw (embedding vector_cosine_ops)
WHERE model = 'text-embedding-3-small';

-- HNSW index for cosine similarity search (Google gemini-embedding-001).
CREATE INDEX idx_embeddings_google ON embeddings USING hnsw (embedding vector_cosine_ops)
WHERE model = 'gemini-embedding-001';

-- +goose Down
DROP INDEX IF EXISTS idx_embeddings_google;
DROP INDEX IF EXISTS idx_embeddings_openai;
DROP TABLE IF EXISTS embeddings;
