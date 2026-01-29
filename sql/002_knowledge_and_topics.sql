-- Knowledge records and topics schema

-- Knowledge records table
CREATE TABLE knowledge_records (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  content TEXT NOT NULL,
  tenant_id VARCHAR(255),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for knowledge_records
CREATE INDEX idx_knowledge_records_tenant_id ON knowledge_records(tenant_id);
CREATE INDEX idx_knowledge_records_created_at ON knowledge_records(created_at);

-- Topics table
CREATE TABLE topics (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  title VARCHAR(255) NOT NULL,
  level INTEGER NOT NULL DEFAULT 1,
  parent_id UUID REFERENCES topics(id) ON DELETE CASCADE,
  tenant_id VARCHAR(255),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for topics
CREATE INDEX idx_topics_tenant_id ON topics(tenant_id);
CREATE INDEX idx_topics_parent_id ON topics(parent_id);
CREATE INDEX idx_topics_level ON topics(level);

-- Partial unique indexes for title uniqueness within (parent_id, tenant_id)
-- Handle NULL parent_id separately since NULL != NULL in PostgreSQL
CREATE UNIQUE INDEX idx_topics_title_parent_tenant 
  ON topics(tenant_id, parent_id, title) WHERE parent_id IS NOT NULL;
CREATE UNIQUE INDEX idx_topics_title_root_tenant 
  ON topics(tenant_id, title) WHERE parent_id IS NULL;
