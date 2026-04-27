-- +goose up
-- Rename feedback_records.user_identifier to user_id.

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'feedback_records'
      AND column_name = 'user_identifier'
  ) AND NOT EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'feedback_records'
      AND column_name = 'user_id'
  ) THEN
    ALTER TABLE feedback_records RENAME COLUMN user_identifier TO user_id;
  END IF;
END $$;
-- +goose StatementEnd

ALTER INDEX IF EXISTS idx_feedback_records_user_identifier RENAME TO idx_feedback_records_user_id;
ALTER INDEX IF EXISTS idx_feedback_records_tenant_user_identifier RENAME TO idx_feedback_records_tenant_user_id;

-- +goose down
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'feedback_records'
      AND column_name = 'user_id'
  ) AND NOT EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name = 'feedback_records'
      AND column_name = 'user_identifier'
  ) THEN
    ALTER TABLE feedback_records RENAME COLUMN user_id TO user_identifier;
  END IF;
END $$;
-- +goose StatementEnd

ALTER INDEX IF EXISTS idx_feedback_records_user_id RENAME TO idx_feedback_records_user_identifier;
ALTER INDEX IF EXISTS idx_feedback_records_tenant_user_id RENAME TO idx_feedback_records_tenant_user_identifier;
