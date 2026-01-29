-- Add field grouping support for composite questions (ranking, matrix, grid)

-- Add new columns
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS field_group_id VARCHAR(255);
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS field_group_label VARCHAR;

-- Add index for field_group_id (commonly filtered on for analytics)
CREATE INDEX IF NOT EXISTS idx_feedback_records_field_group_id ON feedback_records(field_group_id);
