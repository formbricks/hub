-- Clustering jobs table for tracking taxonomy generation jobs and schedules
CREATE TABLE IF NOT EXISTS clustering_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    
    -- Scheduling
    schedule_interval VARCHAR(50), -- 'daily', 'weekly', 'monthly', NULL for one-time
    next_run_at TIMESTAMP WITH TIME ZONE,
    last_run_at TIMESTAMP WITH TIME ZONE,
    
    -- Job result tracking
    last_job_id UUID, -- Most recent job ID from taxonomy service
    last_error TEXT,
    topics_generated INT DEFAULT 0,
    records_processed INT DEFAULT 0,
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    -- Ensure one schedule per tenant
    CONSTRAINT unique_tenant_schedule UNIQUE (tenant_id)
);

-- Index for finding jobs that need to run
CREATE INDEX IF NOT EXISTS idx_clustering_jobs_next_run 
    ON clustering_jobs (next_run_at) 
    WHERE status != 'disabled' AND next_run_at IS NOT NULL;

-- Index for tenant lookups
CREATE INDEX IF NOT EXISTS idx_clustering_jobs_tenant_id 
    ON clustering_jobs (tenant_id);

-- Add trigger to update updated_at
CREATE OR REPLACE FUNCTION update_clustering_jobs_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_clustering_jobs_updated_at ON clustering_jobs;
CREATE TRIGGER trigger_clustering_jobs_updated_at
    BEFORE UPDATE ON clustering_jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_clustering_jobs_updated_at();
