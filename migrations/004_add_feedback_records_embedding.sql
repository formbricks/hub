-- +goose Up
-- Embeddings table: one row per feedback_record per model (e.g. openai text-embedding-3-small).
-- Requires vector extension from 001_initial_schema.sql (pgvector 0.7+ for halfvec).
-- halfvec: 2 bytes per dimension (vs 4 for vector), ~50% storage and index size; recall impact <1% for embeddings.
-- ON DELETE CASCADE: embeddings are deleted when the referenced feedback record is deleted.
CREATE TABLE embeddings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  feedback_record_id UUID NOT NULL REFERENCES feedback_records(id) ON DELETE CASCADE,
  embedding halfvec NOT NULL,
  model TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (feedback_record_id, model)
);

-- HNSW index for cosine similarity search (OpenAI text-embedding-3-small, 1536 dimensions).
-- halfvec_cosine_ops: same semantics as vector_cosine_ops for half-precision column.
-- Other models (e.g. text-embedding-3-large at 3072 dims) are not covered; add a separate index if needed.
CREATE INDEX idx_embeddings_openai ON embeddings USING hnsw ((embedding::halfvec(1536)) halfvec_cosine_ops)
WHERE model = 'text-embedding-3-small';

-- HNSW index for cosine similarity search (Google gemini-embedding-001, 768 dimensions).
CREATE INDEX idx_embeddings_google ON embeddings USING hnsw ((embedding::halfvec(768)) halfvec_cosine_ops)
WHERE model = 'gemini-embedding-001';

-- +goose Down
DROP INDEX IF EXISTS idx_embeddings_google;
DROP INDEX IF EXISTS idx_embeddings_openai;
DROP TABLE IF EXISTS embeddings;
