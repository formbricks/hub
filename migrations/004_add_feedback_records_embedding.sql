-- +goose Up
-- Embeddings table: one row per feedback_record per model. Vector size is fixed (768).
-- Requires vector extension from 001_initial_schema.sql (pgvector 0.7+ for halfvec).
-- halfvec(768): 2 bytes per dimension; recall impact <1% for embeddings.
-- ON DELETE CASCADE: embeddings are deleted when the referenced feedback record is deleted.
CREATE TABLE embeddings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  feedback_record_id UUID NOT NULL REFERENCES feedback_records(id) ON DELETE CASCADE,
  embedding halfvec(768) NOT NULL,
  model TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (feedback_record_id, model)
);

CREATE INDEX idx_embeddings ON embeddings USING hnsw ((embedding halfvec_cosine_ops));

-- +goose Down
DROP INDEX IF EXISTS idx_embeddings;
DROP TABLE IF EXISTS embeddings;
