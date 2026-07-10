-- +goose NO TRANSACTION
-- +goose Up
-- Keep the semantic-search HNSW graph limited to raw/search embeddings. Taxonomy embeddings
-- live in the same table under model keys prefixed with "taxonomy:", but they are fetched by
-- exact model/id joins for taxonomy runs and should not consume HNSW candidate budget.
DROP INDEX CONCURRENTLY IF EXISTS idx_embeddings;
CREATE INDEX CONCURRENTLY idx_embeddings
  ON embeddings USING hnsw (embedding halfvec_cosine_ops)
  WITH (m = 24, ef_construction = 200)
  WHERE model NOT LIKE 'taxonomy:%';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_embeddings;
CREATE INDEX CONCURRENTLY idx_embeddings
  ON embeddings USING hnsw (embedding halfvec_cosine_ops)
  WITH (m = 24, ef_construction = 200);
