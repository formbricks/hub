package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

// TestListPendingEnrichment pins what the reconciler will and will not pick up. Three of these
// four cases are the whole reason the query exists, and each would fail in a different, quiet way:
// a never-attempted record left out means the endpoint reports a remainder nothing drains, a
// retryable failure left out means an outage strands records permanently, and a terminal failure
// left IN means the sweep re-runs a content-filtered record forever at a provider call each tick.
func TestListPendingEnrichment(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Load()
	require.NoError(t, err)
	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	frepo := repository.NewFeedbackRecordsRepository(db)
	rrepo := repository.NewEnrichmentReconcileRepository(db)
	tenant := "pending-probe-" + uuid.NewString()

	pending := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "needs sentiment")
	terminal := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "refused forever")
	transient := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "failed but retryable")
	writeFailed := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "classified but never written")
	doneRec := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "already enriched")

	_, err = db.Exec(ctx, `INSERT INTO feedback_record_enrichment_failures
		(feedback_record_id, enrichment, tenant_id, terminal, reason) VALUES ($1,'sentiment',$2,true,'content_filter')`,
		terminal.ID, tenant)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `INSERT INTO feedback_record_enrichment_failures
		(feedback_record_id, enrichment, tenant_id, terminal, reason) VALUES ($1,'sentiment',$2,false,'provider_error')`,
		transient.ID, tenant)
	require.NoError(t, err)

	// The second non-terminal reason. It is not a provider failure at all — the model answered and
	// the write is what died — but it leaves the record just as un-enriched, so the sweep owes it
	// the same retry. Pinned separately because the exclusion here keys on `terminal`, and a later
	// change that keyed on the reason instead would quietly strand every write failure.
	_, err = db.Exec(ctx, `INSERT INTO feedback_record_enrichment_failures
		(feedback_record_id, enrichment, tenant_id, terminal, reason) VALUES ($1,'sentiment',$2,false,'write_failed')`,
		writeFailed.ID, tenant)
	require.NoError(t, err)

	label, score := models.SentimentPositive, 1.0
	require.NoError(t, frepo.SetSentiment(ctx, doneRec.ID, &label, &score, nil))

	got, err := rrepo.ListPendingEnrichment(ctx, models.EnrichmentNameSentiment, "", 500)
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}

	for _, target := range got {
		if target.TenantID == tenant {
			ids[target.ID] = true
		}
	}

	assert.True(t, ids[pending.ID], "never-attempted record must be pending")
	assert.True(t, ids[transient.ID], "a retryable provider failure must come back as pending")
	assert.True(t, ids[writeFailed.ID], "a failed write is retryable too, and must come back as pending")
	assert.False(t, ids[terminal.ID], "a terminal failure must be excluded or the sweep never ends")
	assert.False(t, ids[doneRec.ID], "an enriched record must not be pending")
	assert.Len(t, ids, 3, "exactly the three pending records, no more")

	// The reconciler is cross-tenant on purpose — a provider outage is deployment-wide — so it
	// must return other tenants' work too. Only the per-tenant filter above hides it here.
	assert.NotEmpty(t, got, "the sweep is global, not tenant-scoped")
}
