package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

// Tenant write serialization tests (ENG-1013).
//
// Determinism: instead of racing goroutines, these tests hold the tenant write
// advisory lock directly on a dedicated connection — exclusively to simulate a
// purge in progress, or shared to simulate an in-flight tenant-owned write —
// and then exercise the real code paths against it.

func newTenantLockDB(ctx context.Context, t *testing.T) *pgxpool.Pool {
	t.Helper()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	t.Cleanup(db.Close)

	return db
}

// holdTenantWriteLock acquires the tenant write advisory lock on its own
// connection and returns an idempotent release func.
func holdTenantWriteLock(
	ctx context.Context, t *testing.T, db *pgxpool.Pool, tenantID string, shared bool,
) func() {
	t.Helper()

	conn, err := db.Acquire(ctx)
	require.NoError(t, err)

	lockTx, err := conn.Begin(ctx)
	require.NoError(t, err)

	lockSQL := `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`
	if shared {
		lockSQL = `SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 0))`
	}

	_, err = lockTx.Exec(ctx, lockSQL, repository.TenantWriteLockKey(tenantID))
	require.NoError(t, err)

	released := false
	release := func() {
		if released {
			return
		}

		released = true

		_ = lockTx.Rollback(ctx)

		conn.Release()
	}

	t.Cleanup(release)

	return release
}

// doTenantLockRequest performs an authenticated request and returns the
// status code and the fully read (and closed) response body.
func doTenantLockRequest(
	ctx context.Context, t *testing.T, client *http.Client, method, requestURL string, payload any,
) (int, []byte) {
	t.Helper()

	var reqBody io.Reader = http.NoBody

	if payload != nil {
		raw, err := json.Marshal(payload)
		require.NoError(t, err)

		reqBody = bytes.NewBuffer(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, reqBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return resp.StatusCode, body
}

// requireTenantWriteConflict asserts a 409 carrying the dedicated retryable
// problem code.
func requireTenantWriteConflict(t *testing.T, status int, body []byte) {
	t.Helper()

	var problem struct {
		Code string `json:"code"`
		Type string `json:"type"`
	}

	require.Equal(t, http.StatusConflict, status, "body: %s", string(body))
	require.NoError(t, json.Unmarshal(body, &problem))
	assert.Equal(t, "tenant_write_conflict", problem.Code)
	assert.Equal(t, "https://hub.formbricks.com/problems/tenant-write-conflict", problem.Type)
}

func feedbackRecordBody(tenantID string) map[string]any {
	return map[string]any{
		"source_type":   "formbricks",
		"submission_id": uuid.NewString(),
		"tenant_id":     tenantID,
		"field_id":      "tenant-lock-field",
		"field_type":    "text",
		"value_text":    "tenant write lock test",
	}
}

func (r *tenantDataEventRecorder) totalEventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.events)
}

func waitForEventCount(t *testing.T, recorder *tenantDataEventRecorder, want int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if recorder.totalEventCount() >= want {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("event count = %d, want at least %d", recorder.totalEventCount(), want)
}

// requireEventCountStays asserts no further events are published for a short
// observation window (rejected writes must publish nothing).
func requireEventCountStays(t *testing.T, recorder *tenantDataEventRecorder, want int) {
	t.Helper()

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if count := recorder.totalEventCount(); count != want {
			t.Fatalf("event count = %d, want %d (rejected writes must publish no events)", count, want)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// TestTenantPurgeWaitsForInFlightWritesThenConflicts covers the purge side:
// an in-flight tenant-owned write (shared lock holder) makes the purge wait up
// to its lock timeout and return a retryable 409; after the writer finishes
// the purge succeeds.
func TestTenantPurgeWaitsForInFlightWritesThenConflicts(t *testing.T) {
	t.Setenv("TENANT_PURGE_LOCK_TIMEOUT_SECONDS", "1")

	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	db := newTenantLockDB(ctx, t)
	client := &http.Client{}
	tenantA := "tenant-lock-purge-" + uuid.NewString()

	createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantA, uuid.NewString(), "tenant-lock-field")

	release := holdTenantWriteLock(ctx, t, db, tenantA, true)

	purgeURL := server.URL + "/v1/tenants/" + url.PathEscape(tenantA) + "/data"

	started := time.Now()
	status, body := doTenantLockRequest(ctx, t, client, http.MethodDelete, purgeURL, nil)
	requireTenantWriteConflict(t, status, body)
	assert.GreaterOrEqual(t, time.Since(started), 900*time.Millisecond,
		"purge should have waited for the lock timeout before conflicting")

	// The failed purge attempt deleted nothing.
	recordsURL := server.URL + "/v1/feedback-records?tenant_id=" + url.QueryEscape(tenantA)
	status, body = doTenantLockRequest(ctx, t, client, http.MethodGet, recordsURL, nil)

	var listed struct {
		Data []models.FeedbackRecord `json:"data"`
	}

	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(body, &listed))
	require.Len(t, listed.Data, 1, "record must survive the conflicted purge")

	// After the in-flight writer drains, the purge succeeds.
	release()

	purgeResp := deleteTenantData(ctx, t, client, server.URL, tenantA)
	assert.Equal(t, int64(1), purgeResp.DeletedFeedbackRecords)
}

// TestTenantWritesRejectedDuringPurge covers the writer side: while the purge
// holds the exclusive tenant lock, every same-tenant mutation returns the
// retryable 409 and publishes no events, while other tenants are unaffected.
func TestTenantWritesRejectedDuringPurge(t *testing.T) {
	eventRecorder := &tenantDataEventRecorder{}

	server, cleanup := setupTestServerWithEventProviders(t, eventRecorder)
	defer cleanup()

	ctx := context.Background()
	db := newTenantLockDB(ctx, t)
	client := &http.Client{}
	tenantA := "tenant-lock-writes-a-" + uuid.NewString()
	tenantB := "tenant-lock-writes-b-" + uuid.NewString()

	// Seed tenant A with a record and a webhook before the purge starts.
	record := createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantA, uuid.NewString(), "tenant-lock-field")
	webhook := createTenantDataWebhook(ctx, t, client, server.URL, tenantA, "tenant-lock-writes")

	// Both seed writes publish events; wait for them so the no-new-events
	// assertion below is anchored at a stable count.
	waitForEventCount(t, eventRecorder, 2)

	baseline := eventRecorder.totalEventCount()

	release := holdTenantWriteLock(ctx, t, db, tenantA, false)
	defer release()

	recordURL := server.URL + "/v1/feedback-records/" + record.ID.String()
	webhookURL := server.URL + "/v1/webhooks/" + webhook.ID.String()

	t.Run("feedback record create rejected", func(t *testing.T) {
		status, body := doTenantLockRequest(
			ctx, t, client, http.MethodPost, server.URL+"/v1/feedback-records", feedbackRecordBody(tenantA))
		requireTenantWriteConflict(t, status, body)
	})

	t.Run("feedback record update rejected", func(t *testing.T) {
		status, body := doTenantLockRequest(ctx, t, client, http.MethodPatch, recordURL, map[string]any{"value_text": "blocked"})
		requireTenantWriteConflict(t, status, body)
	})

	t.Run("feedback record delete rejected", func(t *testing.T) {
		status, body := doTenantLockRequest(ctx, t, client, http.MethodDelete, recordURL, nil)
		requireTenantWriteConflict(t, status, body)
	})

	t.Run("webhook create rejected", func(t *testing.T) {
		status, body := doTenantLockRequest(ctx, t, client, http.MethodPost, server.URL+"/v1/webhooks",
			map[string]any{"url": testWebhookURL + "/tenant-lock-blocked", "tenant_id": tenantA})
		requireTenantWriteConflict(t, status, body)
	})

	t.Run("webhook update rejected", func(t *testing.T) {
		status, body := doTenantLockRequest(ctx, t, client, http.MethodPatch, webhookURL, map[string]any{"enabled": false})
		requireTenantWriteConflict(t, status, body)
	})

	t.Run("webhook delete rejected", func(t *testing.T) {
		status, body := doTenantLockRequest(ctx, t, client, http.MethodDelete, webhookURL, nil)
		requireTenantWriteConflict(t, status, body)
	})

	t.Run("no events published for rejected writes", func(t *testing.T) {
		requireEventCountStays(t, eventRecorder, baseline)
	})

	t.Run("other tenants keep writing", func(t *testing.T) {
		status, body := doTenantLockRequest(
			ctx, t, client, http.MethodPost, server.URL+"/v1/feedback-records", feedbackRecordBody(tenantB))
		require.Equal(t, http.StatusCreated, status, "body: %s", string(body))

		status, body = doTenantLockRequest(ctx, t, client, http.MethodPost, server.URL+"/v1/webhooks",
			map[string]any{"url": testWebhookURL + "/tenant-lock-other", "tenant_id": tenantB})
		require.Equal(t, http.StatusCreated, status, "body: %s", string(body))
	})

	t.Run("writes succeed again after purge releases", func(t *testing.T) {
		release()

		status, body := doTenantLockRequest(ctx, t, client, http.MethodPatch, recordURL, map[string]any{"value_text": "unblocked"})
		require.Equal(t, http.StatusOK, status, "body: %s", string(body))
	})

	cleanupTenantDataBestEffort(ctx, client, server.URL, tenantA)
	cleanupTenantDataBestEffort(ctx, client, server.URL, tenantB)
}

// TestTenantWritesFailFastWhilePurgeQueued covers the load-bearing queueing
// property: the moment a purge is WAITING for the exclusive lock (an in-flight
// writer still holds shared), brand-new same-tenant writes are rejected
// immediately rather than being granted ahead of the queued purge.
func TestTenantWritesFailFastWhilePurgeQueued(t *testing.T) {
	t.Setenv("TENANT_PURGE_LOCK_TIMEOUT_SECONDS", "10")

	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	db := newTenantLockDB(ctx, t)
	client := &http.Client{}
	tenantA := "tenant-lock-queued-" + uuid.NewString()

	createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantA, uuid.NewString(), "tenant-lock-field")

	release := holdTenantWriteLock(ctx, t, db, tenantA, true)
	defer release()

	purgeURL := server.URL + "/v1/tenants/" + url.PathEscape(tenantA) + "/data"
	purgeStatus := make(chan int, 1)

	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, purgeURL, http.NoBody)
		if err != nil {
			purgeStatus <- -1

			return
		}

		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			purgeStatus <- -1

			return
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		purgeStatus <- resp.StatusCode
	}()

	// Wait until the purge's exclusive advisory lock request is queued, scoped to
	// tenantA's key so an unrelated waiter can't satisfy this. For the single-arg
	// advisory form, Postgres stores the int8 key as classid = high 32 bits and
	// objid = low 32 bits, with objsubid = 1.
	require.Eventually(t, func() bool {
		var waiting int

		err := db.QueryRow(ctx, `
			SELECT count(*) FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted AND objsubid = 1
			  AND classid::bigint = ((hashtextextended($1, 0) >> 32) & 4294967295)
			  AND objid::bigint = (hashtextextended($1, 0) & 4294967295)`,
			repository.TenantWriteLockKey(tenantA),
		).Scan(&waiting)

		return err == nil && waiting > 0
	}, 5*time.Second, 20*time.Millisecond, "purge never queued for tenantA's advisory lock")

	// A new same-tenant write must fail fast while the purge is queued.
	status, body := doTenantLockRequest(
		ctx, t, client, http.MethodPost, server.URL+"/v1/feedback-records", feedbackRecordBody(tenantA))
	requireTenantWriteConflict(t, status, body)

	// Release the in-flight writer; the queued purge must now complete.
	release()

	select {
	case status := <-purgeStatus:
		require.Equal(t, http.StatusOK, status, "queued purge should succeed once writers drain")
	case <-time.After(10 * time.Second):
		t.Fatal("queued purge did not finish after writer released")
	}
}

// TestGDPRDeleteByUserDuringPurge covers the cross-tenant erasure flow: the
// unfiltered delete spans tenants A and B; with A under purge the whole call
// conflicts and deletes nothing, while the tenant-B-scoped call proceeds.
func TestGDPRDeleteByUserDuringPurge(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	db := newTenantLockDB(ctx, t)
	client := &http.Client{}
	tenantA := "tenant-lock-gdpr-a-" + uuid.NewString()
	tenantB := "tenant-lock-gdpr-b-" + uuid.NewString()
	userID := "tenant-lock-gdpr-user-" + uuid.NewString()

	createUserRecord := func(tenantID string) models.FeedbackRecord {
		requestBody := feedbackRecordBody(tenantID)
		requestBody["user_id"] = userID

		status, body := doTenantLockRequest(ctx, t, client, http.MethodPost, server.URL+"/v1/feedback-records", requestBody)

		var record models.FeedbackRecord

		require.Equal(t, http.StatusCreated, status, "body: %s", string(body))
		require.NoError(t, json.Unmarshal(body, &record))

		return record
	}

	recordA := createUserRecord(tenantA)
	recordB := createUserRecord(tenantB)

	release := holdTenantWriteLock(ctx, t, db, tenantA, false)
	defer release()

	deleteByUserURL := server.URL + "/v1/feedback-records?user_id=" + url.QueryEscape(userID)

	t.Run("unfiltered delete conflicts and deletes nothing", func(t *testing.T) {
		status, body := doTenantLockRequest(ctx, t, client, http.MethodDelete, deleteByUserURL, nil)
		requireTenantWriteConflict(t, status, body)

		for _, id := range []uuid.UUID{recordA.ID, recordB.ID} {
			getStatus, getBody := doTenantLockRequest(
				ctx, t, client, http.MethodGet, server.URL+"/v1/feedback-records/"+id.String(), nil)
			require.Equal(t, http.StatusOK, getStatus, "body: %s", string(getBody))
		}
	})

	t.Run("tenant-scoped delete for unlocked tenant proceeds", func(t *testing.T) {
		status, body := doTenantLockRequest(ctx, t, client, http.MethodDelete,
			deleteByUserURL+"&tenant_id="+url.QueryEscape(tenantB), nil)

		var deleted models.DeleteFeedbackRecordsByUserResponse

		require.Equal(t, http.StatusOK, status, "body: %s", string(body))
		require.NoError(t, json.Unmarshal(body, &deleted))
		assert.Equal(t, int64(1), deleted.DeletedCount)

		getStatus, getBody := doTenantLockRequest(
			ctx, t, client, http.MethodGet, server.URL+"/v1/feedback-records/"+recordA.ID.String(), nil)
		require.Equal(t, http.StatusOK, getStatus, "body: %s", string(getBody))
	})

	t.Run("unfiltered delete succeeds after purge releases", func(t *testing.T) {
		release()

		status, body := doTenantLockRequest(ctx, t, client, http.MethodDelete, deleteByUserURL, nil)

		var deleted models.DeleteFeedbackRecordsByUserResponse

		require.Equal(t, http.StatusOK, status, "body: %s", string(body))
		require.NoError(t, json.Unmarshal(body, &deleted))
		assert.Equal(t, int64(1), deleted.DeletedCount)
	})

	cleanupTenantDataBestEffort(ctx, client, server.URL, tenantA)
}

// TestWebhookTenantMoveLocksTargetTenant covers tenant-changing webhook
// updates: moving a webhook into a tenant under purge must conflict even
// though the webhook's current tenant is unlocked.
func TestWebhookTenantMoveLocksTargetTenant(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	db := newTenantLockDB(ctx, t)
	client := &http.Client{}
	tenantA := "tenant-lock-move-a-" + uuid.NewString()
	tenantB := "tenant-lock-move-b-" + uuid.NewString()

	webhook := createTenantDataWebhook(ctx, t, client, server.URL, tenantA, "tenant-lock-move")
	webhookURL := server.URL + "/v1/webhooks/" + webhook.ID.String()

	release := holdTenantWriteLock(ctx, t, db, tenantB, false)
	defer release()

	status, body := doTenantLockRequest(ctx, t, client, http.MethodPatch, webhookURL, map[string]any{"tenant_id": tenantB})
	requireTenantWriteConflict(t, status, body)

	release()

	status, body = doTenantLockRequest(ctx, t, client, http.MethodPatch, webhookURL, map[string]any{"tenant_id": tenantB})
	require.Equal(t, http.StatusOK, status, "body: %s", string(body))

	cleanupTenantDataBestEffort(ctx, client, server.URL, tenantB)
}

// TestRepositoryWritesConflictDuringPurge covers the non-HTTP write surfaces
// (worker-driven webhook disable, embedding writes, taxonomy writes) directly
// at the repository layer, which every caller shares.
func TestRepositoryWritesConflictDuringPurge(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	db := newTenantLockDB(ctx, t)
	client := &http.Client{}
	tenantA := "tenant-lock-repo-" + uuid.NewString()

	webhooksRepo := repository.NewWebhooksRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	taxonomyRepo := repository.NewTaxonomyRepository(db)

	record := createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantA, uuid.NewString(), "tenant-lock-field")
	webhook := createTenantDataWebhook(ctx, t, client, server.URL, tenantA, "tenant-lock-repo")

	pendingRun, created, err := taxonomyRepo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
		TaxonomyScope: models.TaxonomyScope{
			TenantID:   tenantA,
			SourceType: "formbricks",
			SourceID:   "tenant-lock-source",
			FieldID:    record.FieldID,
		},
		RecordCount:    1,
		EmbeddingCount: 1,
	})
	require.NoError(t, err)
	require.True(t, created)

	release := holdTenantWriteLock(ctx, t, db, tenantA, false)
	defer release()

	t.Run("webhook delivery disable conflicts", func(t *testing.T) {
		enabled := false
		reason := "max attempts"
		now := time.Now()

		_, err := webhooksRepo.Update(ctx, webhook.ID, &models.UpdateWebhookRequest{
			Enabled:        &enabled,
			DisabledReason: &reason,
			DisabledAt:     &now,
		})
		require.ErrorIs(t, err, huberrors.ErrTenantWriteConflict)

		current, err := webhooksRepo.GetByID(ctx, webhook.ID)
		require.NoError(t, err)
		assert.True(t, current.Enabled, "webhook must stay enabled after rejected disable")
	})

	t.Run("embedding upsert and delete conflict", func(t *testing.T) {
		embedding := make([]float32, models.EmbeddingVectorDimensions)
		embedding[0] = 0.5

		err := embeddingsRepo.Upsert(ctx, record.ID, "model-name", embedding, nil)
		require.ErrorIs(t, err, huberrors.ErrTenantWriteConflict)

		err = embeddingsRepo.DeleteByFeedbackRecordAndModel(ctx, record.ID, "model-name", nil)
		require.ErrorIs(t, err, huberrors.ErrTenantWriteConflict)
	})

	t.Run("taxonomy run create and transitions conflict", func(t *testing.T) {
		_, _, err := taxonomyRepo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
			TaxonomyScope: models.TaxonomyScope{
				TenantID:   tenantA,
				SourceType: "formbricks",
				SourceID:   "tenant-lock-source-2",
				FieldID:    record.FieldID,
			},
			RecordCount:    1,
			EmbeddingCount: 1,
		})
		require.ErrorIs(t, err, huberrors.ErrTenantWriteConflict)

		_, err = taxonomyRepo.MarkRunRunning(ctx, pendingRun.ID, tenantA)
		require.ErrorIs(t, err, huberrors.ErrTenantWriteConflict)
	})

	t.Run("writes succeed after release", func(t *testing.T) {
		release()

		embedding := make([]float32, models.EmbeddingVectorDimensions)
		embedding[0] = 0.5
		require.NoError(t, embeddingsRepo.Upsert(ctx, record.ID, "model-name", embedding, nil))

		_, err := taxonomyRepo.MarkRunRunning(ctx, pendingRun.ID, tenantA)
		require.NoError(t, err)
	})

	t.Run("transition on a non-pending run reports conflict without a second connection", func(t *testing.T) {
		// The run is now 'running'; marking it running again misses the
		// status='pending' filter and routes through transitionError, which must
		// read the run state through the open transaction (not the pool) to build
		// the conflict — exercising the no-second-connection fix.
		_, err := taxonomyRepo.MarkRunRunning(ctx, pendingRun.ID, tenantA)
		require.ErrorIs(t, err, huberrors.ErrConflict)
	})

	cleanupTenantDataBestEffort(ctx, client, server.URL, tenantA)
}

// TestTaxonomyNodeMutationsAreTenantScoped verifies getNodeForUpdate's tenant
// predicate: a node may only be renamed/removed by its owning tenant, so a
// caller can never lock or mutate another tenant's node row.
func TestTaxonomyNodeMutationsAreTenantScoped(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	db := newTenantLockDB(ctx, t)
	client := &http.Client{}
	tenantA := "tenant-node-a-" + uuid.NewString()
	tenantB := "tenant-node-b-" + uuid.NewString()

	taxonomyRepo := repository.NewTaxonomyRepository(db)

	record := createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantA, uuid.NewString(), "tenant-node-field")
	runID := createTenantDataTaxonomyGraph(
		ctx, t, db, tenantA, record.ID, "tenant-node-source-"+uuid.NewString(), record.FieldID,
	)

	var nodeID uuid.UUID
	require.NoError(t, db.QueryRow(ctx,
		`SELECT id FROM taxonomy_nodes WHERE run_id = $1 AND removed_at IS NULL ORDER BY level LIMIT 1`, runID,
	).Scan(&nodeID))

	t.Run("rename from another tenant is not found", func(t *testing.T) {
		_, err := taxonomyRepo.RenameNode(ctx, nodeID, tenantB, "actor", "renamed-by-b")
		require.ErrorIs(t, err, huberrors.ErrNotFound)
	})

	t.Run("remove from another tenant is not found", func(t *testing.T) {
		_, err := taxonomyRepo.RemoveNode(ctx, nodeID, tenantB, "actor")
		require.ErrorIs(t, err, huberrors.ErrNotFound)
	})

	t.Run("rename from the owning tenant succeeds", func(t *testing.T) {
		node, err := taxonomyRepo.RenameNode(ctx, nodeID, tenantA, "actor", "renamed-by-a")
		require.NoError(t, err)
		assert.Equal(t, "renamed-by-a", node.Label)
	})

	cleanupTenantDataBestEffort(ctx, client, server.URL, tenantA)
}

// TestNoOpUpdatesDoNotPublishEvents verifies that an empty PATCH (no fields)
// writes nothing and therefore publishes no "updated" event — so it cannot fire
// tenant-owned side effects, including while the tenant is under a data purge
// (the no-op path intentionally returns the current row without locking).
func TestNoOpUpdatesDoNotPublishEvents(t *testing.T) {
	eventRecorder := &tenantDataEventRecorder{}

	server, cleanup := setupTestServerWithEventProviders(t, eventRecorder)
	defer cleanup()

	ctx := context.Background()
	client := &http.Client{}
	tenantA := "tenant-noop-" + uuid.NewString()

	record := createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantA, uuid.NewString(), "tenant-noop-field")
	webhook := createTenantDataWebhook(ctx, t, client, server.URL, tenantA, "tenant-noop")

	// The two creates publish one event each; anchor the baseline once both land.
	waitForEventCount(t, eventRecorder, 2)
	baseline := eventRecorder.totalEventCount()

	t.Run("empty feedback PATCH returns 200 and publishes no event", func(t *testing.T) {
		status, body := doTenantLockRequest(
			ctx, t, client, http.MethodPatch, server.URL+"/v1/feedback-records/"+record.ID.String(), map[string]any{})
		require.Equal(t, http.StatusOK, status, "body: %s", string(body))
		requireEventCountStays(t, eventRecorder, baseline)
	})

	t.Run("empty webhook PATCH returns 200 and publishes no event", func(t *testing.T) {
		status, body := doTenantLockRequest(
			ctx, t, client, http.MethodPatch, server.URL+"/v1/webhooks/"+webhook.ID.String(), map[string]any{})
		require.Equal(t, http.StatusOK, status, "body: %s", string(body))
		requireEventCountStays(t, eventRecorder, baseline)
	})

	cleanupTenantDataBestEffort(ctx, client, server.URL, tenantA)
}
