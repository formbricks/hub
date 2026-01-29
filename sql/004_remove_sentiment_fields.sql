-- Remove sentiment fields that require separate LLM calls
-- These were added prematurely; keeping only embedding-based enrichment

ALTER TABLE feedback_records DROP COLUMN IF EXISTS sentiment;
ALTER TABLE feedback_records DROP COLUMN IF EXISTS sentiment_score;
ALTER TABLE feedback_records DROP COLUMN IF EXISTS emotion;

-- Drop the indexes that were created for these columns
DROP INDEX IF EXISTS idx_feedback_records_sentiment;
DROP INDEX IF EXISTS idx_feedback_records_emotion;
