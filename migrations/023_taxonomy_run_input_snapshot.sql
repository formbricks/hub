-- +goose up
-- Freeze the exact bounded feedback-record selection handed to the taxonomy
-- service. Completion validates memberships against this snapshot rather than
-- re-running the live "most recent" query, whose window can move while a long
-- generation is in flight.
CREATE TABLE taxonomy_run_input_records (
  run_id UUID NOT NULL,
  tenant_id VARCHAR(255) NOT NULL,
  feedback_record_id UUID NOT NULL,
  sort_order INTEGER NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (run_id, feedback_record_id),
  UNIQUE (run_id, sort_order),
  FOREIGN KEY (run_id, tenant_id) REFERENCES taxonomy_runs(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT taxonomy_run_input_records_tenant_id_required CHECK (btrim(tenant_id) <> ''),
  CONSTRAINT taxonomy_run_input_records_sort_order_nonnegative CHECK (sort_order >= 0)
);

-- +goose down
DROP TABLE IF EXISTS taxonomy_run_input_records;
