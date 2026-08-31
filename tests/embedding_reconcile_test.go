package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

// TestListPendingTaxonomyEmbeddingIDs exercises the real repair-selection query against River and
// enrichment failure state. Each exclusion is part of the safety contract: omitting one either
// duplicates provider work or permanently abandons recoverable records.
func TestListPendingTaxonomyEmbeddingIDs(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	failuresRepo := repository.NewEnrichmentFailuresRepository(db)
	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{})
	require.NoError(t, err)

	tenantID := "embedding-reconcile-" + uuid.NewString()
	model := "taxonomy:reconcile-" + uuid.NewString()

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM river_job WHERE args->>'feedback_record_id' IN (
			SELECT id::text FROM feedback_records WHERE tenant_id = $1
		)`, tenantID)
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, tenantID)
	})

	seed := func(label string) *models.FeedbackRecord {
		t.Helper()

		var record models.FeedbackRecord

		err := db.QueryRow(ctx, `
			INSERT INTO feedback_records (
				source_type, source_id, field_id, field_label, field_type,
				value_text, value_text_translated, tenant_id, submission_id
			)
			VALUES ('formbricks', $1, 'feedback', 'Feedback', 'text'::field_type_enum,
			        $2, $3, $4, $5)
			RETURNING id, tenant_id, updated_at`,
			"source-"+label, "raw "+label, "translated "+label, tenantID, "submission-"+uuid.NewString(),
		).Scan(&record.ID, &record.TenantID, &record.UpdatedAt)
		require.NoError(t, err)

		return &record
	}

	eligible := seed("eligible")
	embedded := seed("already-embedded")
	activeJob := seed("active-job")
	terminalCurrent := seed("terminal-current")
	terminalOldModel := seed("terminal-old-model")
	terminalOldRevision := seed("terminal-old-revision")
	transientFailure := seed("transient-failure")
	transientRetryable := seed("transient-retryable")

	vector := make([]float32, models.EmbeddingVectorDimensions)
	vector[0] = 0.5
	require.NoError(t, embeddingsRepo.Upsert(ctx, embedded.ID, model, vector, nil))

	_, err = riverClient.Insert(ctx, service.FeedbackEmbeddingArgs{
		FeedbackRecordID: activeJob.ID,
		Model:            model,
		InputKind:        models.EmbeddingInputKindTaxonomyTranslated,
		ValueTextHash:    "live-event",
	}, &river.InsertOpts{Queue: service.EmbeddingsQueueName, MaxAttempts: 5})
	require.NoError(t, err)

	recordFailure := func(record *models.FeedbackRecord, contextKey string, terminal bool, reason string) {
		t.Helper()
		require.NoError(t, failuresRepo.RecordFailure(ctx, models.EnrichmentFailure{
			FeedbackRecordID: record.ID,
			TenantID:         tenantID,
			Enrichment:       models.EnrichmentNameTaxonomyEmbedding,
			Terminal:         terminal,
			Reason:           reason,
			Attempts:         5,
			ContextKey:       contextKey,
			SourceUpdatedAt:  &record.UpdatedAt,
		}))
	}

	recordFailure(terminalCurrent, model, true, string(huberrors.TerminalReasonContentFilter))
	recordFailure(terminalOldModel, "taxonomy:retired-model", true, string(huberrors.TerminalReasonContentFilter))
	recordFailure(terminalOldRevision, model, true, string(huberrors.TerminalReasonContentFilter))
	recordFailure(transientFailure, model, false, models.EnrichmentFailureReasonProviderError)
	recordFailure(transientRetryable, model, false, models.EnrichmentFailureReasonProviderError)
	_, err = db.Exec(ctx, `UPDATE feedback_record_enrichment_failures
		SET failed_at = NOW() - interval '1 hour'
		WHERE feedback_record_id = $1 AND enrichment = 'taxonomy_embedding'`, transientRetryable.ID)
	require.NoError(t, err)

	// Simulate an edit after the terminal failure. A marker tied to the old revision must not
	// suppress the new content, even though it was written for the same record and model.
	_, err = db.Exec(ctx, `UPDATE feedback_records SET updated_at = updated_at + interval '1 second' WHERE id = $1`,
		terminalOldRevision.ID)
	require.NoError(t, err)

	// This repository is shared by local integration runs, and a unique model makes every existing
	// fixture look missing. Use a deliberately large inspection limit here; bounded production
	// top-up is asserted separately in the service unit test.
	ids, err := embeddingsRepo.ListPendingTaxonomyEmbeddingIDs(ctx, model, time.Now().Add(-15*time.Minute), 100_000)
	require.NoError(t, err)

	got := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		got[id] = struct{}{}
	}

	for _, record := range []*models.FeedbackRecord{eligible, terminalOldModel, terminalOldRevision, transientRetryable} {
		assert.Contains(t, got, record.ID, "recoverable record %s must be selected", record.ID)
	}

	for _, record := range []*models.FeedbackRecord{embedded, activeJob, terminalCurrent, transientFailure} {
		assert.NotContains(t, got, record.ID, "already handled record %s must not be selected", record.ID)
	}
}
