-- Migration to add theme_id column to feedback_records
-- This enables hierarchical classification: theme (level 1) + topic (level 2)

-- Add theme_id column (references level-1 topics)
ALTER TABLE feedback_records ADD COLUMN IF NOT EXISTS theme_id UUID REFERENCES topics(id) ON DELETE SET NULL;

-- Create index for theme lookups
CREATE INDEX IF NOT EXISTS idx_feedback_records_theme_id ON feedback_records(theme_id);
