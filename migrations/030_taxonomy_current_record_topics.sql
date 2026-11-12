-- +goose up
-- The current directory-taxonomy assignment of every feedback record, for readers outside the API
-- (ENG-6989): Formbricks' Cube semantic layer joins it to feedback_records so charts can group and
-- filter by topic, and BI tools that query this database directly can do the same.
--
-- One row per classified record: its tenant's active directory run, the membership that puts the
-- record in a cluster, the leaf that holds that cluster, and the leaf's three ancestors up to the
-- root. topic_* is the level-1 node and subtopic_* the level-2 node. Labels are read when the view
-- is queried, so a rename, a removed node or a newly activated run shows up on the next read, with
-- nothing to sync.
--
-- The rules are GetFeedbackRecordTaxonomy's (internal/repository/taxonomy_repository.go): only the
-- directory scope's active run counts, and a removed node anywhere on the path, root included,
-- leaves the record unclassified, so it has no row here. That function walks the parents
-- recursively; this view uses fixed joins instead, because every tree is five levels deep (root 0,
-- branches 1-3, leaf 4), and fixed joins let the planner push a reader's tenant and record
-- predicates down into the scan, which a recursive query inside a view would block.
--
-- Two rules about the stored trees make the fixed joins exact. Each tree is five levels deep, and
-- each cluster sits in exactly one leaf. The taxonomy service, the only writer of runs, has refused
-- any other tree since before Hub had directory runs (validate_taxonomy_tree), and Hub checks both
-- rules again when it stores a result (validateTaxonomyResultNodes, since 0.8.6). Renames and
-- removals change neither. So no directory run of another shape exists for these joins to miss or
-- to duplicate. The rules are enforced in code, not by a constraint: a reader that counts rows
-- relies on them.
--
-- Outliers stay in: a record in the outlier cluster ("Uncategorized Feedback") has a row with
-- is_outlier = true, as GetFeedbackRecordTaxonomy also reports it classified. Readers decide
-- whether that counts as a topic; Cube's chart fields leave outliers out.
--
-- At most one row per (tenant_id, feedback_record_id): memberships are unique per run and record,
-- and each cluster sits in exactly one leaf.
--
-- A plain view stores nothing, so it adds no write cost and needs no backfill. It does pin the
-- columns it reads: a later migration that alters or drops one of them must drop this view first
-- and recreate it afterwards.
CREATE VIEW taxonomy_current_record_topics AS
SELECT
  m.tenant_id,
  m.feedback_record_id,
  fr.submission_id,
  ar.run_id,
  topic.id AS topic_node_id,
  topic.label AS topic_label,
  subtopic.id AS subtopic_node_id,
  subtopic.label AS subtopic_label,
  leaf.id AS leaf_node_id,
  c.is_outlier
FROM taxonomy_active_runs ar
INNER JOIN taxonomy_cluster_memberships m
  ON m.run_id = ar.run_id AND m.tenant_id = ar.tenant_id
INNER JOIN taxonomy_clusters c
  ON c.id = m.cluster_id AND c.run_id = m.run_id
INNER JOIN taxonomy_nodes leaf
  ON leaf.run_id = m.run_id AND leaf.cluster_id = m.cluster_id
 AND leaf.node_type = 'leaf' AND leaf.removed_at IS NULL
INNER JOIN taxonomy_nodes l3
  ON l3.run_id = leaf.run_id AND l3.id = leaf.parent_id AND l3.removed_at IS NULL
INNER JOIN taxonomy_nodes subtopic
  ON subtopic.run_id = l3.run_id AND subtopic.id = l3.parent_id AND subtopic.removed_at IS NULL
INNER JOIN taxonomy_nodes topic
  ON topic.run_id = subtopic.run_id AND topic.id = subtopic.parent_id AND topic.removed_at IS NULL
INNER JOIN taxonomy_nodes root
  ON root.run_id = topic.run_id AND root.id = topic.parent_id
 AND root.node_type = 'root' AND root.removed_at IS NULL
INNER JOIN feedback_records fr
  ON fr.id = m.feedback_record_id AND fr.tenant_id = m.tenant_id
WHERE ar.scope_type = 'directory'
  AND ar.source_type = '' AND ar.source_id = '' AND ar.field_id = '';

-- +goose down
DROP VIEW IF EXISTS taxonomy_current_record_topics;
