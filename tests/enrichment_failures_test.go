package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

// insertFailureMarker writes a marker directly, so a test can stamp a tenant_id that DISAGREES
// with the record's. Nothing in production can produce that — the worker reads the tenant off the
// record — but the tests below need it to tell the two candidate tenant boundaries apart, and a
// fixture no code path can produce is exactly what proves the boundary is not the stamp.
func insertFailureMarker(
	t *testing.T, db *pgxpool.Pool,
	record *models.FeedbackRecord, stampedTenant, enrichment string, terminal bool, reason string,
) {
	t.Helper()

	_, err := db.Exec(context.Background(), `
		INSERT INTO feedback_record_enrichment_failures
			(feedback_record_id, enrichment, tenant_id, terminal, reason)
		VALUES ($1, $2, $3, $4, $5)`,
		record.ID, enrichment, stampedTenant, terminal, reason)
	require.NoError(t, err)
}

// TestEnrichmentFailuresPurgedWithTenant proves the failure markers do not outlive the tenant they
// belong to.
//
// The rows carry tenant_id, so surviving one is tenant-scoped data left behind by a purge — a
// retention problem, not untidiness. They are removed by ON DELETE CASCADE rather than by an
// explicit delete in the purge, which diverges from the convention in tenant_data_repository.go,
// so the cascade is what needs proving: drop the foreign key or change the purge to delete by
// tenant instead of by record and this test is what notices.
func TestEnrichmentFailuresPurgedWithTenant(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	frepo := repository.NewFeedbackRecordsRepository(db)
	tenantDataRepo := repository.NewTenantDataRepository(db, 5*time.Second)

	tenant := "enrichment-failures-purge-" + uuid.NewString()
	other := "enrichment-failures-keep-" + uuid.NewString()

	doomed := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "record that failed enrichment")
	survivor := seedEnrichmentRecord(t, frepo, other, models.FieldTypeText, "record in a different tenant")

	// One of each kind, so a cascade that somehow only caught one shape would still fail. The
	// third is stamped with a tenant that is not its record's: it must be purged all the same,
	// because what removes it is the record it hangs off, not the stamp.
	insertFailureMarker(t, db, doomed, tenant, "sentiment", true, "content_filter")
	insertFailureMarker(t, db, doomed, tenant, "emotions", false, "provider_error")
	insertFailureMarker(t, db, doomed, "not-this-tenant-"+uuid.NewString(), "translation", false, "provider_error")
	insertFailureMarker(t, db, survivor, other, "sentiment", true, "refusal")

	// Counted two ways, because a survivor can hide from either one alone. Joining to the record
	// misses an ORPHAN — a marker left behind by a dropped foreign key has no record to join to —
	// and counting the denormalized stamp misses a marker whose stamp is wrong. A row has to
	// evade both to slip through.
	markersOwnedBy := func(tenantID string) int64 {
		t.Helper()

		var count int64
		require.NoError(t, db.QueryRow(ctx, `
			SELECT count(*)
			FROM feedback_record_enrichment_failures f
			JOIN feedback_records fr ON fr.id = f.feedback_record_id
			WHERE fr.tenant_id = $1`, tenantID).Scan(&count))

		return count
	}

	markersStamped := func(tenantID string) int64 {
		t.Helper()

		var count int64
		require.NoError(t, db.QueryRow(ctx,
			`SELECT count(*) FROM feedback_record_enrichment_failures WHERE tenant_id = $1`,
			tenantID).Scan(&count))

		return count
	}

	orphanedMarkers := func() int64 {
		t.Helper()

		var count int64
		require.NoError(t, db.QueryRow(ctx, `
			SELECT count(*)
			FROM feedback_record_enrichment_failures f
			LEFT JOIN feedback_records fr ON fr.id = f.feedback_record_id
			WHERE fr.id IS NULL`).Scan(&count))

		return count
	}

	require.Equal(t, int64(3), markersOwnedBy(tenant), "markers staged, counted by the record they belong to")
	require.Equal(t, int64(2), markersStamped(tenant), "the third marker is deliberately stamped elsewhere")
	require.Equal(t, int64(1), markersOwnedBy(other), "other tenant's marker staged")
	require.Zero(t, orphanedMarkers(), "no orphans before the purge")

	_, err = tenantDataRepo.DeleteByTenant(ctx, tenant)
	require.NoError(t, err)

	assert.Zero(t, markersOwnedBy(tenant), "no failure marker may survive its tenant's purge")
	assert.Zero(t, markersStamped(tenant), "nor may one identified only by its own stamp")
	// The join above cannot see a marker whose record is gone, so the cascade is checked directly:
	// drop the foreign key and the markers survive as orphans while every other count reads zero.
	assert.Zero(t, orphanedMarkers(), "the cascade must remove markers, not orphan them")

	// The purge is tenant-scoped, so a neighbour's markers must be untouched — the same boundary
	// the counting queries rely on.
	assert.Equal(t, int64(1), markersOwnedBy(other), "another tenant's markers must be left alone")

	_, err = tenantDataRepo.DeleteByTenant(ctx, other)
	require.NoError(t, err)
}

// TestCountEnrichmentStatusCountsFailures exercises the failure columns against Postgres. The
// service tests use a fake repository, so this is the only place the SQL itself is checked — and
// the properties that matter here are all properties of the query: that the marker joins do not
// fan out, that a failure stops counting once the record is enriched, and that the tenant boundary
// comes from feedback_records rather than from the markers' own tenant_id column.
func TestCountEnrichmentStatusCountsFailures(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	frepo := repository.NewFeedbackRecordsRepository(db)
	statusRepo := repository.NewEnrichmentStatusRepository(db)
	tenant := "enrichment-failed-counts-" + uuid.NewString()

	mark := func(record *models.FeedbackRecord, enrichment string, terminal bool, reason string) {
		t.Helper()
		insertFailureMarker(t, db, record, tenant, enrichment, terminal, reason)
	}

	transient := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "transient failure")
	mark(transient, "sentiment", false, "provider_error")

	permanent := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "permanent failure")
	mark(permanent, "sentiment", true, "content_filter")

	// A record that failed once and later succeeded. Its marker is deliberately left behind — the
	// design says a success writes no cleanup — so this proves the stale row stops counting.
	recovered := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "failed then recovered")
	mark(recovered, "sentiment", false, "provider_error")

	label, score := models.SentimentPositive, 1.0
	require.NoError(t, frepo.SetSentiment(ctx, recovered.ID, &label, &score, nil))

	// Markers for two different enrichments on one record must not multiply its row.
	both := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "failed twice over")
	mark(both, "sentiment", false, "provider_error")
	mark(both, "emotions", true, "refusal")

	// A record of THIS tenant whose marker is stamped with someone else's tenant_id. Migration 022
	// says that column is there for its index and must never be a tenant predicate; this fixture
	// is what enforces it. Gate any failure count on the marker's own tenant_id — the "obvious"
	// optimization the migration warns about — and this record silently disappears from the tenant
	// that actually owns it, while every other assertion here still passes.
	misstamped := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "marker stamped with the wrong tenant")
	insertFailureMarker(t, db, misstamped, "wrong-stamp-"+uuid.NewString(), "sentiment", false, "provider_error")

	counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
	require.NoError(t, err)

	assert.Equal(t, int64(3), counts.SentimentFailed,
		"transient + the multi-enrichment record + the one whose marker is stamped elsewhere")
	assert.Equal(t, int64(1), counts.SentimentFailedTerminal, "the content-filtered one")
	assert.Equal(t, int64(0), counts.EmotionsFailed)
	assert.Equal(t, int64(1), counts.EmotionsFailedTerminal, "the multi-enrichment record's refusal")

	// The joins must be lookups, not fan-out: five records in, five eligible out. If a record with
	// markers for two enrichments were counted twice this would read 6.
	assert.Equal(t, int64(5), counts.SentimentEligible, "marker joins must not multiply rows")

	// The recovered record is enriched, so its stale marker contributes to nothing.
	assert.Equal(t, int64(1), counts.SentimentDone)

	// Another tenant's markers must be invisible even though the markers table carries a tenant_id
	// of its own — the boundary is feedback_records.tenant_id. The misstamped marker above is the
	// other half of this: one proves a foreign tenant sees nothing, the other proves the owning
	// tenant sees everything of its own regardless of what the stamp says.
	otherTenant := "enrichment-failed-counts-other-" + uuid.NewString()
	otherCounts, err := statusRepo.CountEnrichmentStatus(ctx, otherTenant, "")
	require.NoError(t, err)
	assert.Zero(t, otherCounts.SentimentFailed)
	assert.Zero(t, otherCounts.SentimentFailedTerminal)
}
