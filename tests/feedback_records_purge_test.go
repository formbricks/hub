package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

// newPurgeTestDB opens a pool against the integration database.
func newPurgeTestDB(ctx context.Context, t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	t.Cleanup(db.Close)

	return db
}

// newPurgeService builds the worker-side purge service (no inserter: this exercises the execution
// half, which is what actually touches the database).
func newPurgeService(t *testing.T, db *pgxpool.Pool) *service.FeedbackRecordsPurgeService {
	t.Helper()

	cfg, err := config.Load()
	require.NoError(t, err)

	return service.NewFeedbackRecordsPurgeService(
		repository.NewTenantDataRepository(db, cfg.TenantData.PurgeLockTimeout.Duration()), nil,
	)
}

func countRows(ctx context.Context, t *testing.T, db *pgxpool.Pool, query string, args ...any) int {
	t.Helper()

	var count int

	require.NoError(t, db.QueryRow(ctx, query, args...).Scan(&count))

	return count
}

// seedPurgeEmbedding attaches an embedding to a record so the cascade is observable.
func seedPurgeEmbedding(ctx context.Context, t *testing.T, db *pgxpool.Pool, recordID uuid.UUID) {
	t.Helper()

	_, err := db.Exec(ctx, `
		INSERT INTO embeddings (feedback_record_id, model, embedding)
		VALUES ($1, 'test-model', array_fill(0.1::real, ARRAY[768])::halfvec)`, recordID)
	require.NoError(t, err)
}

// TestPurgeFeedbackRecordsByTenant pins the line that justifies this endpoint existing separately
// from the tenant offboarding purge: the tenant's DATA goes — records, embeddings and the taxonomy
// built on them — while its CONFIGURATION stays. If the configuration half ever regresses, a purge
// silently destroys integrator webhooks and enrichment opt-outs that nothing can restore.
func TestPurgeFeedbackRecordsByTenant(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	purge := newPurgeService(t, db)

	tenantID := "purge-tenant-" + uuid.NewString()
	otherTenantID := "purge-other-" + uuid.NewString()

	scope := models.TaxonomyScope{
		TenantID:   tenantID,
		SourceType: "formbricks",
		SourceID:   "survey-1",
		FieldID:    "field-1",
	}
	ids := seedTaxonomyGraph(ctx, t, db, scope)
	seedPurgeEmbedding(ctx, t, db, ids.FeedbackRecordID)

	// A second tenant with its own graph, to prove the purge does not reach across the boundary.
	otherScope := models.TaxonomyScope{
		TenantID:   otherTenantID,
		SourceType: "formbricks",
		SourceID:   "survey-1",
		FieldID:    "field-1",
	}
	otherIDs := seedTaxonomyGraph(ctx, t, db, otherScope)
	seedPurgeEmbedding(ctx, t, db, otherIDs.FeedbackRecordID)

	// Tenant configuration that must outlive the purge.
	_, err := db.Exec(ctx, `
		INSERT INTO webhooks (tenant_id, url, signing_key, event_types)
		VALUES ($1, 'https://example.test/hook', 'test-signing-key', ARRAY['feedback_record.created'])`, tenantID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO tenant_settings (tenant_id, settings)
		VALUES ($1, '{"target_language":"en-US"}'::jsonb)`, tenantID)
	require.NoError(t, err)

	counts, err := purge.Purge(ctx, tenantID)
	require.NoError(t, err)

	t.Run("reports exact counts for the records and their derived rows", func(t *testing.T) {
		assert.Equal(t, int64(1), counts.DeletedFeedbackRecords)
		assert.Equal(t, int64(1), counts.DeletedEmbeddings)
		assert.Equal(t, int64(1), counts.ClusterMemberships)
		assert.Equal(t, int64(1), counts.Runs)
		assert.Equal(t, int64(1), counts.Clusters)
		assert.Equal(t, int64(3), counts.Nodes)
		assert.Equal(t, int64(1), counts.ActiveRuns)
		assert.Equal(t, int64(1), counts.NodeEvents)
	})

	t.Run("removes the records and everything derived from them", func(t *testing.T) {
		assert.Zero(t, countRows(ctx, t, db,
			`SELECT count(*) FROM feedback_records WHERE tenant_id = $1`, tenantID))
		assert.Zero(t, countRows(ctx, t, db,
			`SELECT count(*) FROM embeddings WHERE feedback_record_id = $1`, ids.FeedbackRecordID))
		assert.Zero(t, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_cluster_memberships WHERE tenant_id = $1`, tenantID))
	})

	// The taxonomy goes with the records it describes. Keeping it would leave a tree the dashboard
	// hides behind its minimum-records gate, whose run header still advertised the old record_count,
	// and which would resurface over unrelated data once the dataset refilled.
	t.Run("removes the taxonomy built on those records", func(t *testing.T) {
		assert.Zero(t, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_runs WHERE id = $1`, ids.RunID))
		assert.Zero(t, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_clusters WHERE id = $1`, ids.ClusterID))
		assert.Zero(t, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_nodes WHERE run_id = $1`, ids.RunID))
		assert.Zero(t, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_active_runs WHERE tenant_id = $1`, tenantID))
		assert.Zero(t, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_node_events WHERE tenant_id = $1`, tenantID))
	})

	// The line between this purge and the offboarding one. Webhooks are integrator-configured
	// directly against the Hub (with signing keys reads never return) and tenant_settings holds
	// enrichment opt-outs that silently revert to enabled once the row is gone — neither is this
	// operation's business.
	t.Run("keeps the tenant's configuration", func(t *testing.T) {
		assert.Equal(t, 1, countRows(ctx, t, db,
			`SELECT count(*) FROM webhooks WHERE tenant_id = $1`, tenantID))
		assert.Equal(t, 1, countRows(ctx, t, db,
			`SELECT count(*) FROM tenant_settings WHERE tenant_id = $1`, tenantID))
	})

	t.Run("leaves other tenants untouched", func(t *testing.T) {
		assert.Equal(t, 1, countRows(ctx, t, db,
			`SELECT count(*) FROM feedback_records WHERE tenant_id = $1`, otherTenantID))
		assert.Equal(t, 1, countRows(ctx, t, db,
			`SELECT count(*) FROM embeddings WHERE feedback_record_id = $1`, otherIDs.FeedbackRecordID))
		assert.Equal(t, 1, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_cluster_memberships WHERE tenant_id = $1`, otherTenantID))
		assert.Equal(t, 1, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_runs WHERE id = $1`, otherIDs.RunID))
		assert.Equal(t, 3, countRows(ctx, t, db,
			`SELECT count(*) FROM taxonomy_nodes WHERE run_id = $1`, otherIDs.RunID))
	})

	t.Run("is idempotent", func(t *testing.T) {
		repeat, err := purge.Purge(ctx, tenantID)
		require.NoError(t, err)

		assert.Zero(t, repeat.DeletedFeedbackRecords)
		assert.Zero(t, repeat.DeletedEmbeddings)
		assert.Zero(t, repeat.ClusterMemberships)
		assert.Zero(t, repeat.Runs)
		assert.Zero(t, repeat.Nodes)
	})
}

// postPurge sends an authenticated purge request (DELETE, tenant in the path) and returns the
// status and body, already read so callers need not manage closing it.
func postPurge(ctx context.Context, t *testing.T, url string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, http.NoBody)
	require.NoError(t, err)

	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, responseBody
}

// countPurgeJobs returns how many purge jobs are queued for a tenant.
func countPurgeJobs(ctx context.Context, t *testing.T, db *pgxpool.Pool, tenantID string) int {
	t.Helper()

	return countRows(ctx, t, db, `
		SELECT count(*) FROM river_job
		WHERE kind = 'feedback_records_purge' AND args->>'tenant_id' = $1`, tenantID)
}

// TestPurgeFeedbackRecordsEndpoint covers the half the DB tests cannot: that the HTTP endpoint
// actually schedules a real, correctly-shaped job. A 202 with nothing on the queue would look like
// success to every caller while deleting nothing, forever.
func TestPurgeFeedbackRecordsEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	tenantID := "purge-endpoint-" + uuid.NewString()

	purgeURL := server.URL + "/v1/tenants/" + tenantID + "/feedback-records"

	status, body := postPurge(ctx, t, purgeURL)
	require.Equal(t, http.StatusAccepted, status, "body: %s", body)

	var accepted models.FeedbackRecordsPurgeAcceptedResponse
	require.NoError(t, json.Unmarshal(body, &accepted))
	assert.Equal(t, tenantID, accepted.TenantID)
	assert.Equal(t, models.FeedbackRecordsPurgeStatusAccepted, accepted.Status)

	t.Run("enqueues exactly one job on the purge queue with the tenant in its args", func(t *testing.T) {
		var queue string

		require.NoError(t, db.QueryRow(ctx, `
			SELECT coalesce(min(queue), '')
			FROM river_job
			WHERE kind = 'feedback_records_purge' AND args->>'tenant_id' = $1`, tenantID,
		).Scan(&queue))

		assert.Equal(t, 1, countPurgeJobs(ctx, t, db, tenantID))
		assert.Equal(t, service.FeedbackRecordsPurgeQueueName, queue)
	})

	// Requesting a purge while one is pending must join it rather than queueing a second, so a
	// double-click cannot stack purges.
	t.Run("a repeat request collapses into the pending purge", func(t *testing.T) {
		repeatStatus, _ := postPurge(ctx, t, purgeURL)
		require.Equal(t, http.StatusAccepted, repeatStatus)

		assert.Equal(t, 1, countPurgeJobs(ctx, t, db, tenantID),
			"a second request must not queue a second purge")
	})

	// The regression test for the shipped bug: with River's DEFAULT unique states a completed purge
	// blocked every later one — the API kept returning 202 while no job was ever created, for as
	// long as the completed row was retained. Only a real River insert can catch this; the unit test
	// can pin the option but not the semantics.
	t.Run("a purge requested after the previous one completed runs again", func(t *testing.T) {
		_, err := db.Exec(ctx, `
			UPDATE river_job SET state = 'completed', finalized_at = now()
			WHERE kind = 'feedback_records_purge' AND args->>'tenant_id' = $1`, tenantID)
		require.NoError(t, err)

		againStatus, _ := postPurge(ctx, t, purgeURL)
		require.Equal(t, http.StatusAccepted, againStatus)

		runnable := countRows(ctx, t, db, `
			SELECT count(*) FROM river_job
			WHERE kind = 'feedback_records_purge' AND args->>'tenant_id' = $1
				AND state NOT IN ('completed', 'cancelled', 'discarded')`, tenantID)

		assert.Equal(t, 1, runnable,
			"a completed purge must not block the next one — the tenant would be unpurgeable")
	})

	// A blank tenant segment must be rejected, not treated as "every tenant". The routing makes an
	// omitted tenant impossible, so this is the only shape left to guard.
	t.Run("rejects a blank tenant", func(t *testing.T) {
		blankStatus, _ := postPurge(ctx, t, server.URL+"/v1/tenants/%20/feedback-records")
		assert.Equal(t, http.StatusBadRequest, blankStatus)
		assert.Zero(t, countPurgeJobs(ctx, t, db, " "))
	})
}

// A purge for a tenant that has never held records must succeed with zero counts rather than error,
// so a caller retrying (or purging an already-empty dataset) gets a clean result.
func TestPurgeFeedbackRecordsByTenantUnknownTenant(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)

	counts, err := newPurgeService(t, db).Purge(ctx, "purge-unknown-"+uuid.NewString())
	require.NoError(t, err)

	assert.Zero(t, counts.DeletedFeedbackRecords)
	assert.Zero(t, counts.DeletedEmbeddings)
	assert.Zero(t, counts.ClusterMemberships)
}

// TestPurgeFeedbackRecordsMultipleBatches exercises the batch loop against a real database: the
// unit tests drive it through a fake that hands back the same transaction every time, so the loop,
// its per-batch commits and the high-water mark are only meaningfully covered here.
func TestPurgeFeedbackRecordsMultipleBatches(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)

	tenantID := "purge-batches-" + uuid.NewString()

	// More than two batches at a batch size of 2000.
	const seeded = 4500

	_, err := db.Exec(ctx, `
		INSERT INTO feedback_records (source_type, field_id, field_label, field_type, value_text, tenant_id, submission_id)
		SELECT 'formbricks', 'f1', 'Feedback', 'text'::field_type_enum, 'r ' || g, $1, 's-' || g
		FROM generate_series(1, $2) g`, tenantID, seeded)
	require.NoError(t, err)

	counts, err := newPurgeService(t, db).Purge(ctx, tenantID)
	require.NoError(t, err)

	assert.Equal(t, int64(seeded), counts.DeletedFeedbackRecords)
	assert.Zero(t, countRows(ctx, t, db,
		`SELECT count(*) FROM feedback_records WHERE tenant_id = $1`, tenantID))
}
