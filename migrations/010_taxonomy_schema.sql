-- +goose up
-- Taxonomy generation artifacts are stored as run-scoped Hub data. Keep them
-- separate from feedback_records so repeated generation never mutates source feedback.
--
-- Source scope and the "no source" bucket:
--   feedback_records.source_id is nullable (see 001_initial_schema.sql), so feedback can
--   exist with no attributed source. Taxonomy must still cover those records. Rather than
--   making source_id nullable inside the composite PK/FK/UNIQUE constraints below (NULL is
--   never equal to NULL, which breaks key matching), taxonomy canonicalizes "no source" to
--   the empty string ''. The Go layer always sends a string, never NULL, so source_id stays
--   NOT NULL here and '' is a valid, comparable key value. Repository queries that read
--   feedback_records use null-safe matching (NULLIF(btrim(source_id), '') IS NOT DISTINCT
--   FROM ...) so the '' taxonomy scope matches NULL/blank feedback rows.
ALTER TABLE feedback_records
  ADD CONSTRAINT feedback_records_id_tenant_unique
  UNIQUE USING INDEX feedback_records_id_tenant_uidx;

CREATE TYPE taxonomy_run_status_enum AS ENUM (
  'pending',
  'running',
  'succeeded',
  'failed',
  'canceled'
);

CREATE TYPE taxonomy_node_type_enum AS ENUM (
  'root',
  'branch',
  'leaf'
);

CREATE TYPE taxonomy_node_event_type_enum AS ENUM (
  'rename',
  'soft_remove'
);

CREATE TABLE taxonomy_runs (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id VARCHAR(255) NOT NULL,
  source_type VARCHAR(255) NOT NULL,
  source_id VARCHAR(255) NOT NULL,
  field_id VARCHAR(255) NOT NULL,
  field_label TEXT,
  status taxonomy_run_status_enum NOT NULL DEFAULT 'pending',
  params JSONB NOT NULL DEFAULT '{}'::jsonb,
  metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
  record_count INTEGER NOT NULL DEFAULT 0,
  embedding_count INTEGER NOT NULL DEFAULT 0,
  cluster_count INTEGER NOT NULL DEFAULT 0,
  node_count INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT taxonomy_runs_tenant_id_required CHECK (btrim(tenant_id) <> ''),
  CONSTRAINT taxonomy_runs_source_type_required CHECK (btrim(source_type) <> ''),
  -- source_id intentionally allows '' (the canonical "no source" bucket); see header note.
  CONSTRAINT taxonomy_runs_field_id_required CHECK (btrim(field_id) <> ''),
  CONSTRAINT taxonomy_runs_record_count_nonnegative CHECK (record_count >= 0),
  CONSTRAINT taxonomy_runs_embedding_count_nonnegative CHECK (embedding_count >= 0),
  CONSTRAINT taxonomy_runs_cluster_count_nonnegative CHECK (cluster_count >= 0),
  CONSTRAINT taxonomy_runs_node_count_nonnegative CHECK (node_count >= 0),
  UNIQUE (id, tenant_id),
  UNIQUE (id, tenant_id, source_type, source_id, field_id)
);

CREATE INDEX idx_taxonomy_runs_tenant_field_created_at
  ON taxonomy_runs (tenant_id, source_type, source_id, field_id, created_at DESC, id);
CREATE INDEX idx_taxonomy_runs_tenant_status_created_at
  ON taxonomy_runs (tenant_id, status, created_at DESC, id);

CREATE TABLE taxonomy_clusters (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  run_id UUID NOT NULL REFERENCES taxonomy_runs(id) ON DELETE CASCADE,
  cluster_key INTEGER NOT NULL,
  label TEXT,
  llm_label TEXT,
  keywords JSONB NOT NULL DEFAULT '[]'::jsonb,
  size INTEGER NOT NULL DEFAULT 0,
  is_outlier BOOLEAN NOT NULL DEFAULT false,
  metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT taxonomy_clusters_size_nonnegative CHECK (size >= 0),
  UNIQUE (run_id, cluster_key),
  UNIQUE (id, run_id)
);

CREATE INDEX idx_taxonomy_clusters_run_size
  ON taxonomy_clusters (run_id, is_outlier, size DESC, id);

CREATE TABLE taxonomy_cluster_memberships (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  run_id UUID NOT NULL,
  tenant_id VARCHAR(255) NOT NULL,
  cluster_id UUID NOT NULL,
  feedback_record_id UUID NOT NULL,
  confidence DOUBLE PRECISION,
  distance DOUBLE PRECISION,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (run_id, tenant_id) REFERENCES taxonomy_runs(id, tenant_id) ON DELETE CASCADE,
  FOREIGN KEY (cluster_id, run_id) REFERENCES taxonomy_clusters(id, run_id) ON DELETE CASCADE,
  FOREIGN KEY (feedback_record_id, tenant_id) REFERENCES feedback_records(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT taxonomy_cluster_memberships_confidence_range
    CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
  CONSTRAINT taxonomy_cluster_memberships_tenant_id_required CHECK (btrim(tenant_id) <> ''),
  UNIQUE (run_id, feedback_record_id)
);

CREATE INDEX idx_taxonomy_cluster_memberships_run_cluster
  ON taxonomy_cluster_memberships (run_id, cluster_id, feedback_record_id);
CREATE INDEX idx_taxonomy_cluster_memberships_feedback_record
  ON taxonomy_cluster_memberships (tenant_id, feedback_record_id, run_id);

CREATE TABLE taxonomy_nodes (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  run_id UUID NOT NULL REFERENCES taxonomy_runs(id) ON DELETE CASCADE,
  parent_id UUID,
  cluster_id UUID,
  node_type taxonomy_node_type_enum NOT NULL,
  label TEXT NOT NULL,
  original_label TEXT,
  description TEXT,
  level INTEGER NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  removed_at TIMESTAMPTZ,
  removed_by VARCHAR(255),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (id, run_id),
  FOREIGN KEY (parent_id, run_id) REFERENCES taxonomy_nodes(id, run_id) ON DELETE CASCADE,
  FOREIGN KEY (cluster_id, run_id) REFERENCES taxonomy_clusters(id, run_id) ON DELETE CASCADE,
  CONSTRAINT taxonomy_nodes_label_required CHECK (btrim(label) <> ''),
  CONSTRAINT taxonomy_nodes_level_nonnegative CHECK (level >= 0),
  CONSTRAINT taxonomy_nodes_tree_shape CHECK (
    (node_type = 'root' AND parent_id IS NULL AND level = 0)
    OR (node_type <> 'root' AND parent_id IS NOT NULL AND level > 0)
  )
);

CREATE UNIQUE INDEX idx_taxonomy_nodes_one_root_per_run
  ON taxonomy_nodes (run_id)
  WHERE parent_id IS NULL;
CREATE INDEX idx_taxonomy_nodes_run_parent_sort
  ON taxonomy_nodes (run_id, parent_id, sort_order, id);
CREATE INDEX idx_taxonomy_nodes_run_visible
  ON taxonomy_nodes (run_id, parent_id, sort_order, id)
  WHERE removed_at IS NULL;
CREATE INDEX idx_taxonomy_nodes_cluster
  ON taxonomy_nodes (cluster_id)
  WHERE cluster_id IS NOT NULL;

CREATE TABLE taxonomy_active_runs (
  tenant_id VARCHAR(255) NOT NULL,
  source_type VARCHAR(255) NOT NULL,
  source_id VARCHAR(255) NOT NULL,
  field_id VARCHAR(255) NOT NULL,
  run_id UUID NOT NULL,
  activated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  activated_by VARCHAR(255),
  PRIMARY KEY (tenant_id, source_type, source_id, field_id),
  FOREIGN KEY (run_id, tenant_id, source_type, source_id, field_id)
    REFERENCES taxonomy_runs(id, tenant_id, source_type, source_id, field_id)
    ON DELETE CASCADE,
  CONSTRAINT taxonomy_active_runs_tenant_id_required CHECK (btrim(tenant_id) <> ''),
  CONSTRAINT taxonomy_active_runs_source_type_required CHECK (btrim(source_type) <> ''),
  -- source_id intentionally allows '' (the canonical "no source" bucket); see header note.
  CONSTRAINT taxonomy_active_runs_field_id_required CHECK (btrim(field_id) <> ''),
  UNIQUE (run_id)
);

CREATE INDEX idx_taxonomy_active_runs_run
  ON taxonomy_active_runs (run_id);

CREATE TABLE taxonomy_node_events (
  id UUID PRIMARY KEY DEFAULT uuidv7(),
  tenant_id VARCHAR(255) NOT NULL,
  source_type VARCHAR(255) NOT NULL,
  source_id VARCHAR(255) NOT NULL,
  field_id VARCHAR(255) NOT NULL,
  run_id UUID NOT NULL,
  node_id UUID NOT NULL,
  event_type taxonomy_node_event_type_enum NOT NULL,
  actor_id VARCHAR(255) NOT NULL,
  old_value JSONB NOT NULL DEFAULT '{}'::jsonb,
  new_value JSONB NOT NULL DEFAULT '{}'::jsonb,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (run_id, tenant_id, source_type, source_id, field_id)
    REFERENCES taxonomy_runs(id, tenant_id, source_type, source_id, field_id)
    ON DELETE CASCADE,
  FOREIGN KEY (node_id, run_id) REFERENCES taxonomy_nodes(id, run_id) ON DELETE CASCADE,
  CONSTRAINT taxonomy_node_events_tenant_id_required CHECK (btrim(tenant_id) <> ''),
  CONSTRAINT taxonomy_node_events_source_type_required CHECK (btrim(source_type) <> ''),
  -- source_id intentionally allows '' (the canonical "no source" bucket); see header note.
  CONSTRAINT taxonomy_node_events_field_id_required CHECK (btrim(field_id) <> ''),
  CONSTRAINT taxonomy_node_events_actor_id_required CHECK (btrim(actor_id) <> '')
);

CREATE INDEX idx_taxonomy_node_events_tenant_created_at
  ON taxonomy_node_events (tenant_id, created_at DESC, id);
CREATE INDEX idx_taxonomy_node_events_run_node_created_at
  ON taxonomy_node_events (run_id, node_id, created_at DESC, id);

-- +goose down
DROP TABLE IF EXISTS taxonomy_node_events;
DROP TABLE IF EXISTS taxonomy_active_runs;
DROP TABLE IF EXISTS taxonomy_nodes;
DROP TABLE IF EXISTS taxonomy_cluster_memberships;
DROP TABLE IF EXISTS taxonomy_clusters;
DROP TABLE IF EXISTS taxonomy_runs;

DROP TYPE IF EXISTS taxonomy_node_event_type_enum;
DROP TYPE IF EXISTS taxonomy_node_type_enum;
DROP TYPE IF EXISTS taxonomy_run_status_enum;

ALTER TABLE feedback_records
  DROP CONSTRAINT IF EXISTS feedback_records_id_tenant_unique;
