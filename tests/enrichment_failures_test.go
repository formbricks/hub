package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

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

	insertFailure := func(record *models.FeedbackRecord, tenantID, enrichment string, terminal bool, reason string) {
		t.Helper()

		_, execErr := db.Exec(ctx, `
			INSERT INTO feedback_record_enrichment_failures
				(feedback_record_id, enrichment, tenant_id, terminal, reason)
			VALUES ($1, $2, $3, $4, $5)`,
			record.ID, enrichment, tenantID, terminal, reason)
		require.NoError(t, execErr)
	}

	// One of each kind, so a cascade that somehow only caught one shape would still fail.
	insertFailure(doomed, tenant, "sentiment", true, "content_filter")
	insertFailure(doomed, tenant, "emotions", false, "provider_error")
	insertFailure(survivor, other, "sentiment", true, "refusal")

	countFor := func(tenantID string) int64 {
		t.Helper()

		var count int64
		require.NoError(t, db.QueryRow(ctx,
			`SELECT count(*) FROM feedback_record_enrichment_failures WHERE tenant_id = $1`,
			tenantID).Scan(&count))

		return count
	}

	require.Equal(t, int64(2), countFor(tenant), "markers staged")
	require.Equal(t, int64(1), countFor(other), "other tenant's marker staged")

	_, err = tenantDataRepo.DeleteByTenant(ctx, tenant)
	require.NoError(t, err)

	assert.Equal(t, int64(0), countFor(tenant), "no failure marker may survive its tenant's purge")
	// The purge is tenant-scoped, so a neighbour's markers must be untouched — the same boundary
	// the counting queries rely on.
	assert.Equal(t, int64(1), countFor(other), "another tenant's markers must be left alone")

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

		_, execErr := db.Exec(ctx, `
			INSERT INTO feedback_record_enrichment_failures
				(feedback_record_id, enrichment, tenant_id, terminal, reason)
			VALUES ($1, $2, $3, $4, $5)`, record.ID, enrichment, tenant, terminal, reason)
		require.NoError(t, execErr)
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

	counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
	require.NoError(t, err)

	assert.Equal(t, int64(2), counts.SentimentFailed, "transient + the multi-enrichment record")
	assert.Equal(t, int64(1), counts.SentimentFailedTerminal, "the content-filtered one")
	assert.Equal(t, int64(0), counts.EmotionsFailed)
	assert.Equal(t, int64(1), counts.EmotionsFailedTerminal, "the multi-enrichment record's refusal")

	// The joins must be lookups, not fan-out: four records in, four eligible out. If a record with
	// markers for two enrichments were counted twice this would read 5.
	assert.Equal(t, int64(4), counts.SentimentEligible, "marker joins must not multiply rows")

	// The recovered record is enriched, so its stale marker contributes to nothing.
	assert.Equal(t, int64(1), counts.SentimentDone)

	// Another tenant's markers must be invisible even though the markers table carries a tenant_id
	// of its own — the boundary is feedback_records.tenant_id.
	otherTenant := "enrichment-failed-counts-other-" + uuid.NewString()
	otherCounts, err := statusRepo.CountEnrichmentStatus(ctx, otherTenant, "")
	require.NoError(t, err)
	assert.Zero(t, otherCounts.SentimentFailed)
	assert.Zero(t, otherCounts.SentimentFailedTerminal)
}
