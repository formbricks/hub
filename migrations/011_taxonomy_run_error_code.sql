-- +goose up
ALTER TABLE taxonomy_runs
  ADD COLUMN error_code VARCHAR(64),
  ADD CONSTRAINT taxonomy_runs_error_code_known CHECK (
    error_code IS NULL OR error_code IN (
      'insufficient_data',
      'service_unavailable',
      'generation_failed',
      'invalid_output',
      'internal_error'
    )
  );

-- +goose down
ALTER TABLE taxonomy_runs
  DROP CONSTRAINT IF EXISTS taxonomy_runs_error_code_known,
  DROP COLUMN IF EXISTS error_code;
