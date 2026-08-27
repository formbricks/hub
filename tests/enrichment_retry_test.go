package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

func postRetry(t *testing.T, serverURL, tenantID string, body any) (int, models.EnrichmentRetryResponse) {
	t.Helper()

	var reader io.Reader = http.NoBody

	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)

		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		serverURL+"/v1/tenants/"+tenantID+"/enrichments/retry", reader)
	require.NoError(t, err)

	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var decoded models.EnrichmentRetryResponse
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}

	return resp.StatusCode, decoded
}

func outcomeFor(resp models.EnrichmentRetryResponse, enrichment string) models.EnrichmentRetryResult {
	for _, result := range resp.Results {
		if result.Enrichment == enrichment {
			return result
		}
	}

	return models.EnrichmentRetryResult{}
}

// TestEnrichmentRetryClearsTerminalMarkers covers the endpoint's reason for existing: the automatic
// sweep will not revive a terminal record, by design, so this is the only way one gets another
// chance after its text is edited or a provider changes its policy.
func TestEnrichmentRetryClearsTerminalMarkers(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	server, cleanupServer := setupTestServer(t)

	defer cleanupServer()

	frepo := repository.NewFeedbackRecordsRepository(db)
	reconcile := repository.NewEnrichmentReconcileRepository(db)

	tenant := "retry-clears-" + uuid.NewString()

	terminal := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "refused forever")
	transient := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "failed but retryable")
	insertFailureMarker(t, db, terminal.ID, tenant, "sentiment", true, "content_filter")
	insertFailureMarker(t, db, transient.ID, tenant, "sentiment", false, "provider_error")

	// Before: the terminal record is excluded from the pending set, which is what the sweep reads.
	pendingBefore := pendingIDs(ctx, t, reconcile, tenant)
	require.NotContains(t, pendingBefore, terminal.ID, "a terminal record is not swept")
	require.Contains(t, pendingBefore, transient.ID, "a retryable one is")

	status, resp := postRetry(t, server.URL, tenant, map[string]any{"enrichments": []string{"sentiment"}})
	require.Equal(t, http.StatusAccepted, status)

	result := outcomeFor(resp, "sentiment")
	assert.Equal(t, models.RetryOutcomeCleared, result.Outcome)
	assert.Equal(t, int64(1), result.Cleared, "the terminal marker, not the retryable one")

	// After: it is pending again, and the retryable one is untouched — clearing terminal flags is
	// not the same as clearing every failure.
	pendingAfter := pendingIDs(ctx, t, reconcile, tenant)
	assert.Contains(t, pendingAfter, terminal.ID, "the cleared record is swept again")
	assert.Contains(t, pendingAfter, transient.ID)

	remaining := countRowsIn(ctx, t, db,
		`SELECT count(*) FROM feedback_record_enrichment_failures f
		 JOIN feedback_records fr ON fr.id = f.feedback_record_id
		 WHERE fr.tenant_id = $1 AND NOT f.terminal`, tenant)
	assert.Equal(t, int64(1), remaining, "the transient marker survives")
}

// TestEnrichmentRetryCooldown is the security control, not a nicety. Terminal records are the ones
// guaranteed to fail again, so an unbounded clear is call → clear → sweep → all fail → repeat, at
// one provider invocation per record per cycle, from one authenticated caller. The Hub has no rate
// limiting anywhere else.
func TestEnrichmentRetryCooldown(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	server, cleanupServer := setupTestServer(t)

	defer cleanupServer()

	frepo := repository.NewFeedbackRecordsRepository(db)

	tenant := "retry-cooldown-" + uuid.NewString()
	record := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "refused forever")
	insertFailureMarker(t, db, record.ID, tenant, "sentiment", true, "content_filter")

	_, first := postRetry(t, server.URL, tenant, map[string]any{"enrichments": []string{"sentiment"}})
	require.Equal(t, models.RetryOutcomeCleared, outcomeFor(first, "sentiment").Outcome)

	status, second := postRetry(t, server.URL, tenant, map[string]any{"enrichments": []string{"sentiment"}})
	require.Equal(t, http.StatusAccepted, status, "a refusal is still an accepted request")

	refused := outcomeFor(second, "sentiment")
	assert.Equal(t, models.RetryOutcomeCoolingDown, refused.Outcome)
	assert.Positive(t, refused.RetryAfterSeconds,
		"the wait is reported: a caller who cannot tell 'nothing to clear' from 'refused' just calls again")

	// The window is 2s in the test server, so this proves it EXPIRES rather than latching — a
	// cooldown that never released would be a worse bug than no cooldown.
	insertFailureMarker(t, db, record.ID, tenant, "sentiment", true, "refusal")
	time.Sleep(2100 * time.Millisecond)

	_, third := postRetry(t, server.URL, tenant, map[string]any{"enrichments": []string{"sentiment"}})
	assert.Equal(t, models.RetryOutcomeCleared, outcomeFor(third, "sentiment").Outcome,
		"the cooldown expires")
}

// TestEnrichmentRetryRefusesDisabledEnrichment: clearing markers for a pipeline the tenant switched
// off would queue work the worker's own gate skips, so the records get re-marked and the caller has
// burned their cooldown for nothing.
func TestEnrichmentRetryRefusesDisabledEnrichment(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	server, cleanupServer := setupTestServer(t)

	defer cleanupServer()

	frepo := repository.NewFeedbackRecordsRepository(db)

	tenant := "retry-disabled-" + uuid.NewString()
	record := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "refused forever")
	insertFailureMarker(t, db, record.ID, tenant, "sentiment", true, "content_filter")

	_, err := db.Exec(ctx, `INSERT INTO tenant_settings (tenant_id, settings)
		VALUES ($1, '{"sentiment_enabled":false}'::jsonb)`, tenant)
	require.NoError(t, err)

	status, resp := postRetry(t, server.URL, tenant, map[string]any{"enrichments": []string{"sentiment"}})
	require.Equal(t, http.StatusAccepted, status)

	result := outcomeFor(resp, "sentiment")
	assert.Equal(t, models.RetryOutcomeDisabled, result.Outcome)
	assert.Equal(t, models.DisabledReasonSwitchedOff, result.DisabledReason,
		"the same vocabulary the status endpoint uses")
	assert.Zero(t, result.Cleared)

	still := countRowsIn(ctx, t, db,
		`SELECT count(*) FROM feedback_record_enrichment_failures WHERE feedback_record_id = $1`, record.ID)
	assert.Equal(t, int64(1), still, "nothing was cleared")

	// And the refusal must not have consumed the cooldown, or a tenant who switches the enrichment
	// back on would be locked out for an hour by a call that did nothing.
	_, err = db.Exec(ctx, `UPDATE tenant_settings SET settings = '{"sentiment_enabled":true}'::jsonb
		WHERE tenant_id = $1`, tenant)
	require.NoError(t, err)

	_, after := postRetry(t, server.URL, tenant, map[string]any{"enrichments": []string{"sentiment"}})
	assert.Equal(t, models.RetryOutcomeCleared, outcomeFor(after, "sentiment").Outcome,
		"a refused retry does not spend the cooldown")
}

// TestEnrichmentRetryDefaultsToEveryEnrichment: the common call is a UI button with no body.
func TestEnrichmentRetryDefaultsToEveryEnrichment(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	server, cleanupServer := setupTestServer(t)

	defer cleanupServer()

	frepo := repository.NewFeedbackRecordsRepository(db)

	tenant := "retry-all-" + uuid.NewString()
	record := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "refused everywhere")
	insertFailureMarker(t, db, record.ID, tenant, "sentiment", true, "content_filter")
	insertFailureMarker(t, db, record.ID, tenant, "emotions", true, "refusal")

	status, resp := postRetry(t, server.URL, tenant, nil)
	require.Equal(t, http.StatusAccepted, status)
	require.Len(t, resp.Results, 3, "every enrichment gets an outcome, including ones with nothing to do")

	assert.Equal(t, int64(1), outcomeFor(resp, "sentiment").Cleared)
	assert.Equal(t, int64(1), outcomeFor(resp, "emotions").Cleared)
	assert.Equal(t, models.RetryOutcomeCleared, outcomeFor(resp, "translation").Outcome,
		"nothing to clear is still a clear, not an error")
	assert.Zero(t, outcomeFor(resp, "translation").Cleared)
}

// TestEnrichmentRetryRejectsUnknownEnrichment: silently dropping a misspelled name would answer
// with a cheerful 202 and an empty result list, and the caller would conclude there was nothing to
// retry.
func TestEnrichmentRetryRejectsUnknownEnrichment(t *testing.T) {
	server, cleanupServer := setupTestServer(t)

	defer cleanupServer()

	tenant := "retry-unknown-" + uuid.NewString()

	status, _ := postRetry(t, server.URL, tenant, map[string]any{"enrichments": []string{"sentimnet"}})
	assert.Equal(t, http.StatusBadRequest, status)
}

// pendingIDs reads what the reconcile sweep would pick up for one tenant.
func pendingIDs(
	ctx context.Context, t *testing.T, repo *repository.EnrichmentReconcileRepository, tenant string,
) map[uuid.UUID]bool {
	t.Helper()

	targets, err := repo.ListPendingEnrichment(ctx, models.EnrichmentNameSentiment, "", 500)
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}

	for _, target := range targets {
		if target.TenantID == tenant {
			ids[target.ID] = true
		}
	}

	return ids
}

// TestEnrichmentRetryIsTenantScoped is the alternate-path regression AGENTS.md requires for a bulk
// mutation: tenant A's clear must not touch tenant B's markers, even a marker whose denormalized
// stamp lies about its tenant — the boundary is the record's tenant, joined through, and this is
// the query that would leak if that predicate were dropped.
func TestEnrichmentRetryIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	server, cleanupServer := setupTestServer(t)

	defer cleanupServer()

	frepo := repository.NewFeedbackRecordsRepository(db)

	tenantA := "retry-scope-a-" + uuid.NewString()
	tenantB := "retry-scope-b-" + uuid.NewString()

	recordA := seedEnrichmentRecord(t, frepo, tenantA, models.FieldTypeText, "tenant A terminal")
	recordB := seedEnrichmentRecord(t, frepo, tenantB, models.FieldTypeText, "tenant B terminal")
	insertFailureMarker(t, db, recordA.ID, tenantA, "sentiment", true, "content_filter")
	insertFailureMarker(t, db, recordB.ID, tenantB, "sentiment", true, "refusal")

	// And tenant B's record carrying tenant A's STAMP — unproducible in production, seeded to prove
	// the delete keys on the record's tenant rather than the marker's column.
	misstamped := seedEnrichmentRecord(t, frepo, tenantB, models.FieldTypeText, "B record, A stamp")
	insertFailureMarker(t, db, misstamped.ID, tenantA, "sentiment", true, "recitation")

	status, resp := postRetry(t, server.URL, tenantA, map[string]any{"enrichments": []string{"sentiment"}})
	require.Equal(t, http.StatusAccepted, status)
	assert.Equal(t, int64(1), outcomeFor(resp, "sentiment").Cleared,
		"tenant A owns exactly one terminal marker; the misstamped one belongs to B's record")

	for record, want := range map[uuid.UUID]int64{recordB.ID: 1, misstamped.ID: 1, recordA.ID: 0} {
		got := countRowsIn(ctx, t, db,
			`SELECT count(*) FROM feedback_record_enrichment_failures WHERE feedback_record_id = $1`, record)
		assert.Equal(t, want, got, "record %s", record)
	}
}

// TestEnrichmentRetryCooldownIsAtomic races N concurrent retries for the same (tenant, enrichment).
// The claim is decided inside the cooldown row's lock, so exactly one may win — a check-then-act
// version let every racer through, which quietly voided the one bound this endpoint has.
func TestEnrichmentRetryCooldownIsAtomic(t *testing.T) {
	ctx := context.Background()
	db := newPurgeTestDB(ctx, t)
	server, cleanupServer := setupTestServer(t)

	defer cleanupServer()

	frepo := repository.NewFeedbackRecordsRepository(db)
	tenant := "retry-atomic-" + uuid.NewString()
	record := seedEnrichmentRecord(t, frepo, tenant, models.FieldTypeText, "raced terminal")
	insertFailureMarker(t, db, record.ID, tenant, "sentiment", true, "content_filter")

	const racers = 8

	var (
		racersGroup sync.WaitGroup
		cleared     atomic.Int64
		cooling     atomic.Int64
	)

	for range racers {
		racersGroup.Go(func() {
			status, resp := postRetry(t, server.URL, tenant, map[string]any{"enrichments": []string{"sentiment"}})
			if status != http.StatusAccepted {
				return
			}

			switch outcomeFor(resp, "sentiment").Outcome {
			case models.RetryOutcomeCleared:
				cleared.Add(1)
			case models.RetryOutcomeCoolingDown:
				cooling.Add(1)
			case models.RetryOutcomeDisabled:
				// Sentiment is configured in the test server; a disabled outcome would fail the
				// count assertions below, which is the right failure.
			}
		})
	}

	racersGroup.Wait()

	assert.Equal(t, int64(1), cleared.Load(), "exactly one racer claims the window")
	assert.Equal(t, int64(racers-1), cooling.Load(), "every other racer is told to wait")
}
