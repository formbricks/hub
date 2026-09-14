package tests

import (
	"context"
	"fmt"
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
//
// Takes the record id rather than the record so the purge tests, which seed through the taxonomy
// helpers and hold only ids, use the same fixture as the counting tests.
func insertFailureMarker(
	t *testing.T, db *pgxpool.Pool,
	recordID uuid.UUID, stampedTenant, enrichment string, terminal bool, reason string,
) {
	t.Helper()

	_, err := db.Exec(context.Background(), `
		INSERT INTO feedback_record_enrichment_failures
			(feedback_record_id, enrichment, tenant_id, terminal, reason)
		VALUES ($1, $2, $3, $4, $5)`,
		recordID, enrichment, stampedTenant, terminal, reason)
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
	insertFailureMarker(t, db, doomed.ID, tenant, "sentiment", true, "content_filter")
	insertFailureMarker(t, db, doomed.ID, tenant, "emotions", false, "provider_error")
	insertFailureMarker(t, db, doomed.ID, "not-this-tenant-"+uuid.NewString(), "translation", false, "provider_error")
	insertFailureMarker(t, db, survivor.ID, other, "sentiment", true, "refusal")

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
		insertFailureMarker(t, db, record.ID, tenant, enrichment, terminal, reason)
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

	// A record of THIS tenant whose marker is stamped with someone else's tenant_id. Nothing can
	// produce that — the worker reads the tenant off the record — so it exists purely to pin which
	// predicate governs.
	//
	// The counting query keys the marker JOIN on the stamp as well as on the record, for the index
	// (see enrichmentFailureJoins), so this marker is NOT counted: the accepted trade, and the safe
	// direction, since the record just reads as still in progress. What must never change is the
	// other half — the stamped tenant sees nothing, because the boundary is the RECORD's tenant in
	// the outer WHERE, not the stamp.
	misstampedTenant := "wrong-stamp-" + uuid.NewString()
	misstamped := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "marker stamped with the wrong tenant")
	insertFailureMarker(t, db, misstamped.ID, misstampedTenant, "sentiment", false, "provider_error")

	counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
	require.NoError(t, err)

	assert.Equal(t, int64(2), counts.SentimentFailed,
		"transient + the multi-enrichment record; the marker stamped elsewhere does not attach")
	assert.Equal(t, int64(1), counts.SentimentFailedTerminal, "the content-filtered one")
	assert.Equal(t, int64(0), counts.EmotionsFailed)
	assert.Equal(t, int64(1), counts.EmotionsFailedTerminal, "the multi-enrichment record's refusal")

	// The joins must be lookups, not fan-out: five records in, five eligible out. If a record with
	// markers for two enrichments were counted twice this would read 6.
	assert.Equal(t, int64(5), counts.SentimentEligible, "marker joins must not multiply rows")

	// The recovered record is enriched, so its stale marker contributes to nothing.
	assert.Equal(t, int64(1), counts.SentimentDone)

	// A tenant with no records of its own sees nothing, however many markers name it. This is the
	// boundary assertion: the stamped tenant owns no feedback record, so the outer
	// fr.tenant_id predicate is the only thing that can be keeping its count at zero.
	stamped, err := statusRepo.CountEnrichmentStatus(ctx, misstampedTenant, "")
	require.NoError(t, err)
	assert.Zero(t, stamped.SentimentEligible, "a tenant with no records has nothing eligible")
	assert.Zero(t, stamped.SentimentFailed, "and cannot see a marker merely because it names them")
	assert.Zero(t, stamped.SentimentFailedTerminal)

	// And a wholly unrelated tenant likewise.
	otherTenant := "enrichment-failed-counts-other-" + uuid.NewString()
	otherCounts, err := statusRepo.CountEnrichmentStatus(ctx, otherTenant, "")
	require.NoError(t, err)
	assert.Zero(t, otherCounts.SentimentFailed)
	assert.Zero(t, otherCounts.SentimentFailedTerminal)
}

// TestEnrichmentFailureMarkerConstraints holds the database to the rule the Go types only assert
// by convention: terminal and reason are ONE decision, and the enrichment name is closed.
//
// The CHECK is what stops a bug writing terminal = true with a retryable reason, which would make
// anything that re-enqueues unfinished work skip that record permanently — a record silently
// abandoned because of a typo. A constraint nothing tries to violate is a constraint nobody
// notices the loss of.
func TestEnrichmentFailureMarkerConstraints(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	frepo := repository.NewFeedbackRecordsRepository(db)
	tenant := "enrichment-marker-checks-" + uuid.NewString()
	record := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "constraint fixture")

	insert := func(enrichment string, terminal bool, reason string) error {
		_, execErr := db.Exec(ctx, `
			INSERT INTO feedback_record_enrichment_failures
				(feedback_record_id, enrichment, tenant_id, terminal, reason)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (feedback_record_id, enrichment) DO UPDATE SET
				terminal = EXCLUDED.terminal, reason = EXCLUDED.reason`,
			record.ID, enrichment, tenant, terminal, reason)
		if execErr != nil {
			return fmt.Errorf("insert marker: %w", execErr)
		}

		return nil
	}

	accepted := map[string]struct {
		terminal bool
		reason   string
	}{
		"content filter is permanent": {true, "content_filter"},
		"refusal is permanent":        {true, "refusal"},
		"length is permanent":         {true, "length"},
		"recitation is permanent":     {true, "recitation"},
		"a provider outage is not":    {false, "provider_error"},
		"nor is a failed write":       {false, "write_failed"},
	}

	for name, want := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			require.NoError(t, insert("sentiment", want.terminal, want.reason))
		})
	}

	rejected := map[string]struct {
		enrichment string
		terminal   bool
		reason     string
	}{
		// The dangerous direction: a permanent-looking marker for a failure a retry would fix,
		// which has anything that re-enqueues unfinished work skip the record for good.
		"a retryable reason marked permanent": {"sentiment", true, "provider_error"},
		"a failed write marked permanent":     {"sentiment", true, "write_failed"},
		// And the mirror, which has the reconciler retrying something that can never succeed.
		"a permanent reason marked retryable": {"sentiment", false, "content_filter"},
		"a reason nothing defines":            {"sentiment", false, "something_new"},
		// Widening the enrichment set must take a migration, not just worker code.
		"an enrichment this table does not hold": {"embeddings", false, "provider_error"},
	}

	for name, bad := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			require.Error(t, insert(bad.enrichment, bad.terminal, bad.reason))
		})
	}
}

// TestStaleMarkerStopsCountingWhenTheRecordMovesOn is the fail → succeed → INVALIDATE case.
//
// The advisory-marker design rested on "a stale row stops counting the moment the record is done",
// which quietly assumes done is permanent. It is not: editing value_text nulls the translation
// columns, and changing a tenant's target_language shifts the effective target so an
// already-translated record stops matching it. Either would revive a marker from a failure that
// was resolved long ago, and the endpoint would then report `failed` for work that is merely
// re-queued — the wrong-progress symptom this feature exists to remove, reintroduced by its own
// bookkeeping. The successful write now deletes the marker, which is what closes it.
func TestStaleMarkerStopsCountingWhenTheRecordMovesOn(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	frepo := repository.NewFeedbackRecordsRepository(db)
	statusRepo := repository.NewEnrichmentStatusRepository(db)
	tenant := "stale-marker-" + uuid.NewString()

	_, err = db.Exec(ctx, `INSERT INTO tenant_settings (tenant_id, settings)
		VALUES ($1, '{"target_language":"de-DE"}'::jsonb)`, tenant)
	require.NoError(t, err)

	record := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "failed once, then translated")

	// 1. It failed.
	insertFailureMarker(t, db, record.ID, tenant, "translation", false, "provider_error")

	counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
	require.NoError(t, err)
	require.Equal(t, int64(1), counts.TranslationFailed, "a fresh failure counts")

	// 2. It later succeeded. No cleanup write — that is the design — so the marker is still there.
	translated := "übersetzt"
	require.NoError(t, frepo.SetTranslation(ctx, record.ID, &translated, "de-DE", "",
		func(*string) bool { return true }))

	counts, err = statusRepo.CountEnrichmentStatus(ctx, tenant, "")
	require.NoError(t, err)
	require.Equal(t, int64(1), counts.TranslationDone)
	require.Zero(t, counts.TranslationFailed, "a resolved failure stops counting")

	// The successful write REMOVED the marker. This is the load-bearing half: leaving it in place
	// is what let a resolved failure come back when the record was later un-enriched.
	markers := countRowsIn(ctx, t, db,
		`SELECT count(*) FROM feedback_record_enrichment_failures WHERE feedback_record_id = $1`, record.ID)
	require.Zero(t, markers, "success clears the marker it resolved")

	// 3. The tenant changes its target language. The record is untouched, but it is no longer
	//    translated INTO THE CURRENT TARGET, so it is pending again — and the marker must not come
	//    back with it.
	_, err = db.Exec(ctx, `UPDATE tenant_settings SET settings = '{"target_language":"fr-FR"}'::jsonb
		WHERE tenant_id = $1`, tenant)
	require.NoError(t, err)

	counts, err = statusRepo.CountEnrichmentStatus(ctx, tenant, "")
	require.NoError(t, err)
	assert.Zero(t, counts.TranslationDone, "a new target means the old translation no longer counts")
	assert.Zero(t, counts.TranslationFailed,
		"and the resolved failure must NOT resurrect: this work is queued, not failed")
	assert.Zero(t, counts.TranslationFailedTerminal)

	// 4. A failure recorded against the record as it now stands does count — the mechanism removes
	//    resolved failures, not current ones.
	insertFailureMarker(t, db, record.ID, tenant, "translation", true, "content_filter")

	counts, err = statusRepo.CountEnrichmentStatus(ctx, tenant, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts.TranslationFailedTerminal,
		"a failure that has not been resolved describes the record as it stands")
}

// TestCountFailedRecordsAggregateIsGated covers the cross-tenant gauge, which had no test at all
// and is the one query here with no tenant predicate.
//
// It must agree with what the endpoint reports for the same tenants, or an operator sees failures
// on a dashboard that the API says are zero and cannot reconcile the two.
func TestCountFailedRecordsAggregateIsGated(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	frepo := repository.NewFeedbackRecordsRepository(db)
	statusRepo := repository.NewEnrichmentStatusRepository(db)

	countFor := func(enrichment string, terminal bool, counts []repository.FailedRecordCount) int64 {
		for _, c := range counts {
			if c.Enrichment == enrichment && c.Terminal == terminal {
				return c.Count
			}
		}

		return 0
	}

	before, err := statusRepo.CountFailedRecordsAggregate(ctx, "")
	require.NoError(t, err)

	// A tenant that switched sentiment off. Its failures are history, not work.
	off := "agg-off-" + uuid.NewString()
	_, err = db.Exec(ctx, `INSERT INTO tenant_settings (tenant_id, settings)
		VALUES ($1, '{"sentiment_enabled":false}'::jsonb)`, off)
	require.NoError(t, err)

	offRecord := seedEnrichmentRecord(t, frepo, off, models.FieldTypeText, "sentiment is switched off here")
	insertFailureMarker(t, db, offRecord.ID, off, "sentiment", false, "provider_error")

	// A record whose text was blanked: eligible for nothing, so owed nothing.
	blanked := "agg-blank-" + uuid.NewString()
	blankRecord := seedEnrichmentRecord(t, frepo, blanked, models.FieldTypeText, "text that will be removed")
	insertFailureMarker(t, db, blankRecord.ID, blanked, "sentiment", false, "provider_error")
	_, err = db.Exec(ctx, `UPDATE feedback_records SET value_text = '   ' WHERE id = $1`, blankRecord.ID)
	require.NoError(t, err)

	// And a real, current failure that must be counted.
	live := "agg-live-" + uuid.NewString()
	liveRecord := seedEnrichmentRecord(t, frepo, live, models.FieldTypeText, "genuinely failed sentiment")
	insertFailureMarker(t, db, liveRecord.ID, live, "sentiment", false, "provider_error")

	after, err := statusRepo.CountFailedRecordsAggregate(ctx, "")
	require.NoError(t, err)

	delta := countFor("sentiment", false, after) - countFor("sentiment", false, before)
	assert.Equal(t, int64(1), delta,
		"only the live failure counts: not the switched-off tenant's, not the blanked record's")

	// The gauge and the endpoint must not disagree for the same tenant.
	for _, tenant := range []string{off, blanked} {
		counts, statusErr := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
		require.NoError(t, statusErr)
		assert.Zero(t, counts.SentimentFailed, "%s: the endpoint reports nothing failed", tenant)
	}
}

func TestCountFailedRecordsAggregateIncludesOnlyCurrentTaxonomyEmbeddingFailures(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	failureRepo := repository.NewEnrichmentFailuresRepository(db)
	feedbackRepo := repository.NewFeedbackRecordsRepository(db)
	statusRepo := repository.NewEnrichmentStatusRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	tenantID := "aggregate-taxonomy-failure-" + uuid.NewString()
	model := "taxonomy:aggregate-" + uuid.NewString()

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, tenantID)
	})

	countFor := func(counts []repository.FailedRecordCount) int64 {
		for _, count := range counts {
			if count.Enrichment == models.EnrichmentNameTaxonomyEmbedding && !count.Terminal {
				return count.Count
			}
		}

		return 0
	}

	before, err := statusRepo.CountFailedRecordsAggregate(ctx, model)
	require.NoError(t, err)

	record := seedEnrichmentRecord(t, feedbackRepo, tenantID, models.FieldTypeText, "embedding provider timed out")
	require.NoError(t, failureRepo.RecordFailure(ctx, models.EnrichmentFailure{
		FeedbackRecordID: record.ID,
		TenantID:         tenantID,
		Enrichment:       models.EnrichmentNameTaxonomyEmbedding,
		Reason:           models.EnrichmentFailureReasonProviderError,
		Attempts:         5,
		ContextKey:       model,
		SourceUpdatedAt:  &record.UpdatedAt,
	}))

	afterFailure, err := statusRepo.CountFailedRecordsAggregate(ctx, model)
	require.NoError(t, err)
	assert.Equal(t, int64(1), countFor(afterFailure)-countFor(before))

	embedding := make([]float32, models.EmbeddingVectorDimensions)
	embedding[0] = 0.25
	require.NoError(t, embeddingsRepo.Upsert(ctx, record.ID, model, embedding, nil))

	afterSuccess, err := statusRepo.CountFailedRecordsAggregate(ctx, model)
	require.NoError(t, err)
	assert.Equal(t, countFor(before), countFor(afterSuccess),
		"a stale marker must stop counting once the exact embedding exists")
}

func countRowsIn(ctx context.Context, t *testing.T, db *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()

	var count int64

	require.NoError(t, db.QueryRow(ctx, query, args...).Scan(&count))

	return count
}
