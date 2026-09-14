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

	got, err := rrepo.ListPendingEnrichment(ctx, models.EnrichmentNameSentiment, "", pendingPageLimit)
	require.NoError(t, err)
	require.Less(t, len(got), pendingPageLimit,
		"the shared test database now fills this page, so the seeded records may be truncated off "+
			"the end — raise pendingPageLimit rather than trusting the assertions below")

	ids := map[uuid.UUID]bool{}

	for _, target := range got {
		if target.TenantID == tenant {
			ids[target.ID] = true
		}
	}

	assert.True(t, ids[pending.ID], "never-attempted record must be pending")

	// A record whose enrichment already has a job in flight — on ANY queue — is not pending: it is
	// scheduled. This exclusion, not River uniqueness, is what stops the sweep double-enqueueing
	// work the event path or a backfill command is already handling (their jobs carry no matching
	// unique key), and it is what keeps an in-backoff head from starving the tail.
	inflight := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "already queued elsewhere")
	_, err = db.Exec(ctx, `
		INSERT INTO river_job (state, queue, kind, priority, args, max_attempts)
		VALUES ('retryable', 'sentiments', 'feedback_sentiment', 1,
			jsonb_build_object('feedback_record_id', $1::text, 'value_text_hash', 'abc'), 3)`, inflight.ID)
	require.NoError(t, err)

	// And one whose job FINISHED (completed): finished jobs are not in flight, so if the record is
	// somehow still un-enriched it must be pending again — the exclusion must not overreach.
	finished := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "job completed but record untouched")
	_, err = db.Exec(ctx, `
		INSERT INTO river_job (state, queue, kind, priority, args, max_attempts, finalized_at)
		VALUES ('completed', 'sentiments', 'feedback_sentiment', 1,
			jsonb_build_object('feedback_record_id', $1::text, 'value_text_hash', 'abc'), 3, NOW())`, finished.ID)
	require.NoError(t, err)

	got2, err := rrepo.ListPendingEnrichment(ctx, models.EnrichmentNameSentiment, "", pendingPageLimit)
	require.NoError(t, err)
	require.Less(t, len(got2), pendingPageLimit,
		"the shared test database now fills this page, so the seeded records may be truncated off "+
			"the end — raise pendingPageLimit rather than trusting the assertions below")

	ids2 := map[uuid.UUID]bool{}

	for _, target := range got2 {
		if target.TenantID == tenant {
			ids2[target.ID] = true
		}
	}

	assert.False(t, ids2[inflight.ID], "a record with an in-flight job must not be re-enqueued")
	assert.True(t, ids2[finished.ID], "a finished job is not in flight; the record is pending again")
	assert.True(t, ids[transient.ID], "a retryable provider failure must come back as pending")
	assert.True(t, ids[writeFailed.ID], "a failed write is retryable too, and must come back as pending")
	assert.False(t, ids[terminal.ID], "a terminal failure must be excluded or the sweep never ends")
	assert.False(t, ids[doneRec.ID], "an enriched record must not be pending")
	assert.Len(t, ids, 3, "exactly the three pending records, no more")

	// The reconciler is cross-tenant on purpose — a provider outage is deployment-wide — so it
	// must return other tenants' work too. Only the per-tenant filter above hides it here.
	assert.NotEmpty(t, got, "the sweep is global, not tenant-scoped")
}

// TestQueueDepthCountsRetryableJobs binds the state set the depth control uses to the one the
// pending set uses. They have to be the same set, and the reason is not obvious from either alone.
//
// A record whose job is retryable is excluded from the pending set — correctly, it is already
// being handled. If that same job is also invisible to the depth count, the sweep sees an empty
// queue and tops it up to TargetDepth with DIFFERENT records, on top of the ones backing off.
// Whenever the retry window exceeds the sweep interval — reachable by raising *_MAX_ATTEMPTS,
// since River backs off by roughly attempt^4 seconds — that repeats every tick and the queue grows
// without limit, which is exactly the unbounded river_job growth TargetDepth exists to prevent.
//
// Scheduled is pinned alongside it because River moves a job retryable -> scheduled as its backoff
// elapses, so a set that counted only one of the two would leave the same hole open for part of
// every retry cycle.
func TestQueueDepthCountsRetryableJobs(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	rrepo := repository.NewEnrichmentReconcileRepository(db)

	queue := "depth-probe-" + uuid.NewString()

	insert := func(state string, finalized bool) {
		if finalized {
			_, err := db.Exec(ctx, `INSERT INTO river_job (state, queue, kind, priority, args, max_attempts, finalized_at)
				VALUES ($1, $2, 'feedback_sentiment', 1, '{}'::jsonb, 3, NOW())`, state, queue)
			require.NoError(t, err)

			return
		}

		_, err := db.Exec(ctx, `INSERT INTO river_job (state, queue, kind, priority, args, max_attempts)
			VALUES ($1, $2, 'feedback_sentiment', 1, '{}'::jsonb, 3)`, state, queue)
		require.NoError(t, err)
	}

	for _, state := range []string{"available", "pending", "retryable", "running", "scheduled"} {
		insert(state, false)
	}

	// Finished work must NOT count: it occupies no capacity, and counting it would wedge the queue
	// at its target forever once TargetDepth jobs had ever completed on it.
	insert("completed", true)
	insert("cancelled", true)

	depths, err := rrepo.CountInFlightByQueue(ctx, []string{queue})
	require.NoError(t, err)

	assert.Equal(t, int64(5), depths[queue],
		"every in-flight state occupies the lane, retryable included — otherwise the sweep tops up "+
			"on top of work that is already backing off and TargetDepth bounds nothing")
}
