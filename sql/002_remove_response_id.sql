-- Remove response_id column from feedback_records table
-- This field is no longer needed

-- Drop the index first
DROP INDEX IF EXISTS idx_feedback_records_response_id;

-- Drop the column
ALTER TABLE feedback_records DROP COLUMN IF EXISTS response_id;
