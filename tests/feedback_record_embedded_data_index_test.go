package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

const embeddedDataIndexName = "idx_feedback_records_embedded_data"

// twoKeyEmbeddedDataPredicate is the shape Formbricks dashboards send for two embedded-data
// filters: one containment predicate per key, ANDed.
const twoKeyEmbeddedDataPredicate = `metadata->'embedded_data' @> $1::jsonb AND metadata->'embedded_data' @> $2::jsonb`

func withEmbeddedData(t *testing.T, data map[string]string) func(*models.CreateFeedbackRecordRequest) {
	t.Helper()

	raw, err := json.Marshal(map[string]any{"embedded_data": data, "url": "https://example.com"})
	require.NoError(t, err)

	return func(req *models.CreateFeedbackRecordRequest) { req.Metadata = raw }
}

// TestEmbeddedDataIndex_Definition pins the index migration 028 creates: a valid GIN index with
// jsonb_path_ops on exactly metadata->'embedded_data'. A query must use that same expression to
// match it, so a change to either side silently drops dashboards back to a scan.
func TestEmbeddedDataIndex_Definition(t *testing.T) {
	env := newFilterTestEnv(t, "ed-index-def")

	var (
		def   string
		valid bool
	)

	err := env.db.QueryRow(t.Context(), `
		SELECT pg_get_indexdef(i.indexrelid), i.indisvalid
		FROM pg_index i
		WHERE i.indexrelid = $1::regclass`, embeddedDataIndexName).Scan(&def, &valid)
	require.NoError(t, err)

	assert.True(t, valid, "an interrupted CREATE INDEX CONCURRENTLY leaves an INVALID index")
	assert.Contains(t, def, "USING gin")
	assert.Contains(t, def, "(metadata -> 'embedded_data'::text)")
	assert.Contains(t, def, "jsonb_path_ops")
}

// TestEmbeddedDataIndex_TwoKeyFilterUsesIndex asserts the planner can answer a two-key
// containment filter from the index, with both keys as index conditions. The test table is far too
// small for the planner to prefer an index on cost, so sequential scans are disabled for the
// transaction, and the tenant predicate is left out so no tenant index can stand in: what is checked
// is that the predicate matches the index, not the cost model. On a large table the planner ANDs
// this index with the tenant index (see the migration's measurements).
func TestEmbeddedDataIndex_TwoKeyFilterUsesIndex(t *testing.T) {
	env := newFilterTestEnv(t, "ed-index-plan")
	ctx := t.Context()

	planTx, err := env.db.Begin(ctx)
	require.NoError(t, err)

	t.Cleanup(func() { _ = planTx.Rollback(ctx) })

	_, err = planTx.Exec(ctx, `SET LOCAL enable_seqscan = off`)
	require.NoError(t, err)

	rows, err := planTx.Query(ctx, "EXPLAIN (COSTS OFF) SELECT id FROM feedback_records WHERE "+twoKeyEmbeddedDataPredicate,
		`{"plan":"pro"}`, `{"region":"eu"}`)
	require.NoError(t, err)

	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)

	plan := strings.Join(lines, "\n")
	require.Contains(t, plan, "Bitmap Index Scan on "+embeddedDataIndexName, plan)

	var indexCond string

	for _, line := range lines {
		if strings.Contains(line, "Index Cond:") {
			indexCond = line
		}
	}

	assert.Contains(t, indexCond, `'{"plan": "pro"}'::jsonb`, plan)
	assert.Contains(t, indexCond, `'{"region": "eu"}'::jsonb`, plan)
}

// TestEmbeddedDataIndex_TwoKeyFilterResults checks that records written through the repository
// store embedded data where the index expression reads it, and that the two-key filter returns
// exactly the records carrying both values, matched case-sensitively.
func TestEmbeddedDataIndex_TwoKeyFilterResults(t *testing.T) {
	env := newFilterTestEnv(t, "ed-index-rows")
	ctx := t.Context()

	t.Cleanup(func() {
		_, _ = env.db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, env.tenant)
	})

	match := env.seed(t, withEmbeddedData(t, map[string]string{"plan": "pro", "region": "eu", "seats": "10"}))
	env.seed(t, withEmbeddedData(t, map[string]string{"plan": "pro", "region": "us"}))
	env.seed(t, withEmbeddedData(t, map[string]string{"plan": "free", "region": "eu"}))
	env.seed(t, withEmbeddedData(t, map[string]string{"plan": "Pro", "region": "eu"}))
	env.seed(t) // no metadata at all

	rows, err := env.db.Query(ctx, "SELECT id FROM feedback_records WHERE tenant_id = $3 AND "+twoKeyEmbeddedDataPredicate,
		`{"plan":"pro"}`, `{"region":"eu"}`, env.tenant)
	require.NoError(t, err)

	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	require.NoError(t, err)

	assert.Equal(t, []uuid.UUID{match.ID}, ids)
}
