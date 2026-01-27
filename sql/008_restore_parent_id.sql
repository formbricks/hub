-- Restore parent_id column for explicit Level 1 → Level 2 hierarchy
-- Embeddings are used for feedback classification, not for topic hierarchy

-- Add parent_id column back to topics
ALTER TABLE topics
ADD COLUMN IF NOT EXISTS parent_id UUID REFERENCES topics(id) ON DELETE CASCADE;

-- Create index for efficient parent lookups
CREATE INDEX IF NOT EXISTS idx_topics_parent_id ON topics(parent_id);

-- Add constraint: Level 2 topics must have a parent_id, Level 1 topics must not
-- (This is enforced at application level, not DB level, for flexibility)
