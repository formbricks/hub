-- Migration: Add composite indexes and constraints for topics hierarchy
-- This improves query performance for tenant-scoped hierarchy queries
-- and prevents topics from referencing themselves.

-- Composite index for tenant-scoped hierarchy queries
-- Optimizes: queries filtering by tenant_id AND parent_id together
CREATE INDEX IF NOT EXISTS idx_topics_tenant_parent 
    ON topics(tenant_id, parent_id);

-- Prevent topics from referencing themselves (circular reference protection)
-- Note: This only prevents direct self-reference. Deeper cycles are prevented
-- by the application logic (parent_id is immutable after creation).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint 
        WHERE conname = 'chk_topics_no_self_reference'
    ) THEN
        ALTER TABLE topics 
            ADD CONSTRAINT chk_topics_no_self_reference 
            CHECK (parent_id IS NULL OR parent_id != id);
    END IF;
END $$;
