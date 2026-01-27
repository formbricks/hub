-- Migration: Remove explicit hierarchy in favor of pgvector-based similarity
-- This removes theme_id from feedback_records and parent_id from topics.
-- Level 2 topics will now be associated with Level 1 topics via embedding similarity.

-- Remove theme_id from feedback_records
ALTER TABLE feedback_records DROP COLUMN IF EXISTS theme_id;
DROP INDEX IF EXISTS idx_feedback_records_theme_id;

-- Remove parent_id from topics
ALTER TABLE topics DROP COLUMN IF EXISTS parent_id;
DROP INDEX IF EXISTS idx_topics_parent_id;
