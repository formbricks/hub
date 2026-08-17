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
