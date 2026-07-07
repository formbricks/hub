-- +goose up
CREATE TYPE taxonomy_scope_type_enum AS ENUM (
  'field',
  'directory'
);

ALTER TABLE taxonomy_runs
  ADD COLUMN scope_type taxonomy_scope_type_enum NOT NULL DEFAULT 'field';
ALTER TABLE taxonomy_active_runs
  ADD COLUMN scope_type taxonomy_scope_type_enum NOT NULL DEFAULT 'field';
ALTER TABLE taxonomy_node_events
  ADD COLUMN scope_type taxonomy_scope_type_enum NOT NULL DEFAULT 'field';

ALTER TABLE taxonomy_runs
  DROP CONSTRAINT IF EXISTS taxonomy_runs_source_type_required,
  DROP CONSTRAINT IF EXISTS taxonomy_runs_field_id_required,
  ADD CONSTRAINT taxonomy_runs_scope_identity_unique
    UNIQUE (id, tenant_id, scope_type, source_type, source_id, field_id),
  ADD CONSTRAINT taxonomy_runs_scope_shape CHECK (
    (
      scope_type = 'field'
      AND btrim(source_type) <> ''
      AND btrim(field_id) <> ''
    )
    OR (
      scope_type = 'directory'
      AND source_type = ''
      AND source_id = ''
      AND field_id = ''
    )
  );

CREATE INDEX idx_taxonomy_runs_tenant_scope_created_at
  ON taxonomy_runs (tenant_id, scope_type, source_type, source_id, field_id, created_at DESC, id);

-- +goose StatementBegin
DO $$
DECLARE
  constraint_name text;
BEGIN
  SELECT c.conname INTO constraint_name
  FROM pg_constraint c
  WHERE c.conrelid = 'taxonomy_active_runs'::regclass
    AND c.contype = 'f'
    AND c.confrelid = 'taxonomy_runs'::regclass;

  IF constraint_name IS NOT NULL THEN
    EXECUTE format('ALTER TABLE taxonomy_active_runs DROP CONSTRAINT %I', constraint_name);
  END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE taxonomy_active_runs
  DROP CONSTRAINT IF EXISTS taxonomy_active_runs_pkey,
  DROP CONSTRAINT IF EXISTS taxonomy_active_runs_source_type_required,
  DROP CONSTRAINT IF EXISTS taxonomy_active_runs_field_id_required,
  ADD CONSTRAINT taxonomy_active_runs_pkey
    PRIMARY KEY (tenant_id, scope_type, source_type, source_id, field_id),
  ADD CONSTRAINT taxonomy_active_runs_scope_shape CHECK (
    (
      scope_type = 'field'
      AND btrim(source_type) <> ''
      AND btrim(field_id) <> ''
    )
    OR (
      scope_type = 'directory'
      AND source_type = ''
      AND source_id = ''
      AND field_id = ''
    )
  ),
  ADD CONSTRAINT taxonomy_active_runs_scope_fkey
    FOREIGN KEY (run_id, tenant_id, scope_type, source_type, source_id, field_id)
    REFERENCES taxonomy_runs(id, tenant_id, scope_type, source_type, source_id, field_id)
    ON DELETE CASCADE;

-- +goose StatementBegin
DO $$
DECLARE
  constraint_name text;
BEGIN
  SELECT c.conname INTO constraint_name
  FROM pg_constraint c
  WHERE c.conrelid = 'taxonomy_node_events'::regclass
    AND c.contype = 'f'
    AND c.confrelid = 'taxonomy_runs'::regclass;

  IF constraint_name IS NOT NULL THEN
    EXECUTE format('ALTER TABLE taxonomy_node_events DROP CONSTRAINT %I', constraint_name);
  END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE taxonomy_node_events
  DROP CONSTRAINT IF EXISTS taxonomy_node_events_source_type_required,
  DROP CONSTRAINT IF EXISTS taxonomy_node_events_field_id_required,
  ADD CONSTRAINT taxonomy_node_events_scope_shape CHECK (
    (
      scope_type = 'field'
      AND btrim(source_type) <> ''
      AND btrim(field_id) <> ''
    )
    OR (
      scope_type = 'directory'
      AND source_type = ''
      AND source_id = ''
      AND field_id = ''
    )
  ),
  ADD CONSTRAINT taxonomy_node_events_scope_fkey
    FOREIGN KEY (run_id, tenant_id, scope_type, source_type, source_id, field_id)
    REFERENCES taxonomy_runs(id, tenant_id, scope_type, source_type, source_id, field_id)
    ON DELETE CASCADE;

-- +goose down
DELETE FROM taxonomy_runs WHERE scope_type = 'directory';

ALTER TABLE taxonomy_active_runs
  DROP CONSTRAINT IF EXISTS taxonomy_active_runs_scope_fkey;
ALTER TABLE taxonomy_node_events
  DROP CONSTRAINT IF EXISTS taxonomy_node_events_scope_fkey;

ALTER TABLE taxonomy_active_runs
  DROP CONSTRAINT IF EXISTS taxonomy_active_runs_pkey,
  DROP CONSTRAINT IF EXISTS taxonomy_active_runs_scope_shape,
  ADD CONSTRAINT taxonomy_active_runs_pkey
    PRIMARY KEY (tenant_id, source_type, source_id, field_id),
  ADD CONSTRAINT taxonomy_active_runs_source_type_required CHECK (btrim(source_type) <> ''),
  ADD CONSTRAINT taxonomy_active_runs_field_id_required CHECK (btrim(field_id) <> ''),
  DROP COLUMN IF EXISTS scope_type,
  ADD CONSTRAINT taxonomy_active_runs_run_scope_fkey
    FOREIGN KEY (run_id, tenant_id, source_type, source_id, field_id)
    REFERENCES taxonomy_runs(id, tenant_id, source_type, source_id, field_id)
    ON DELETE CASCADE;

ALTER TABLE taxonomy_node_events
  DROP CONSTRAINT IF EXISTS taxonomy_node_events_scope_shape,
  ADD CONSTRAINT taxonomy_node_events_source_type_required CHECK (btrim(source_type) <> ''),
  ADD CONSTRAINT taxonomy_node_events_field_id_required CHECK (btrim(field_id) <> ''),
  DROP COLUMN IF EXISTS scope_type,
  ADD CONSTRAINT taxonomy_node_events_run_scope_fkey
    FOREIGN KEY (run_id, tenant_id, source_type, source_id, field_id)
    REFERENCES taxonomy_runs(id, tenant_id, source_type, source_id, field_id)
    ON DELETE CASCADE;

DROP INDEX IF EXISTS idx_taxonomy_runs_tenant_scope_created_at;

ALTER TABLE taxonomy_runs
  DROP CONSTRAINT IF EXISTS taxonomy_runs_scope_shape,
  DROP CONSTRAINT IF EXISTS taxonomy_runs_scope_identity_unique,
  ADD CONSTRAINT taxonomy_runs_source_type_required CHECK (btrim(source_type) <> ''),
  ADD CONSTRAINT taxonomy_runs_field_id_required CHECK (btrim(field_id) <> ''),
  DROP COLUMN IF EXISTS scope_type;

DROP TYPE IF EXISTS taxonomy_scope_type_enum;
