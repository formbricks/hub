-- +goose Up
-- B-tree index on model to help filter embeddings by model before/while using the HNSW vector index.
CREATE INDEX idx_embeddings_model ON embeddings (model);

-- Make tenant_id required for feedback_records. Backfill existing NULLs with empty string for legacy data.
UPDATE feedback_records SET tenant_id = '' WHERE tenant_id IS NULL;
ALTER TABLE feedback_records ALTER COLUMN tenant_id SET NOT NULL;

-- Recreate HNSW index with m=24, ef_construction=200 for better recall (defaults are m=16, ef_construction=64).
DROP INDEX IF EXISTS idx_embeddings;
CREATE INDEX idx_embeddings ON embeddings USING hnsw (embedding halfvec_cosine_ops) WITH (m = 24, ef_construction = 200);

-- +goose Down
DROP INDEX IF EXISTS idx_embeddings;
CREATE INDEX idx_embeddings ON embeddings USING hnsw (embedding halfvec_cosine_ops);

ALTER TABLE feedback_records ALTER COLUMN tenant_id DROP NOT NULL;

DROP INDEX IF EXISTS idx_embeddings_model;
