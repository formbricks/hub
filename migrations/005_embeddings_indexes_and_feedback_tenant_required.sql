-- +goose NO TRANSACTION
-- +goose Up
-- B-tree index on model to help filter embeddings by model before/while using the HNSW vector index.
-- CONCURRENTLY avoids blocking writes on large tables (requires NO TRANSACTION).
CREATE INDEX CONCURRENTLY idx_embeddings_model ON embeddings (model);

-- Make tenant_id required for feedback_records. Backfill NULLs to a sentinel to preserve isolation and provenance.
-- Optional column marks rows that originally had NULL tenant_id for future reconciliation.
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS tenant_id_was_null BOOLEAN NOT NULL DEFAULT false;
UPDATE feedback_records SET tenant_id_was_null = true WHERE tenant_id IS NULL;
UPDATE feedback_records SET tenant_id = 'migrated-null-tenant' WHERE tenant_id IS NULL;
ALTER TABLE feedback_records ALTER COLUMN tenant_id SET NOT NULL;

-- Recreate HNSW index with m=24, ef_construction=200 for better recall (defaults are m=16, ef_construction=64).
DROP INDEX CONCURRENTLY IF EXISTS idx_embeddings;
CREATE INDEX CONCURRENTLY idx_embeddings ON embeddings USING hnsw (embedding halfvec_cosine_ops) WITH (m = 24, ef_construction = 200);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_embeddings;
CREATE INDEX CONCURRENTLY idx_embeddings ON embeddings USING hnsw (embedding halfvec_cosine_ops);

ALTER TABLE feedback_records ALTER COLUMN tenant_id DROP NOT NULL;
UPDATE feedback_records SET tenant_id = NULL WHERE tenant_id = 'migrated-null-tenant';
ALTER TABLE feedback_records DROP COLUMN IF EXISTS tenant_id_was_null;

DROP INDEX CONCURRENTLY IF EXISTS idx_embeddings_model;
