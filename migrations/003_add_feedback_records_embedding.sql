-- +goose Up
-- Add embedding column for OpenAI text-embedding-3-small (1536 dimensions).
-- Requires vector extension from 001_initial_schema.sql.
ALTER TABLE feedback_records
  ADD COLUMN embedding vector(1536);

-- +goose Down
ALTER TABLE feedback_records
  DROP COLUMN IF EXISTS embedding;
