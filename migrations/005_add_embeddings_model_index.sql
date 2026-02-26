-- +goose Up
-- B-tree index on model to help filter embeddings by model before/while using the HNSW vector index.
CREATE INDEX idx_embeddings_model ON embeddings (model);

-- +goose Down
DROP INDEX IF EXISTS idx_embeddings_model;
