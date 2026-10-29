package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
)

var errEnqueueBoundary = errors.New("enqueue boundary: simulated failure")

// failingTxInserter stands in for anything that stops the job insert between the record write and
// its commit: a lost connection, a statement timeout, a process killed mid-transaction.
type failingTxInserter struct{}

func (failingTxInserter) InsertManyTx(
	context.Context, pgx.Tx, []river.InsertManyParams,
) ([]*rivertype.JobInsertResult, error) {
	return nil, errEnqueueBoundary
}

// eventRecorder captures published events so a test can match their IDs against job args.
type eventRecorder struct{ events []service.Event }

func (r *eventRecorder) PublishEvent(ctx context.Context, eventType datatypes.EventType, data any) {
	r.Publish(ctx, service.NewEvent(eventType, data, nil))
}

func (r *eventRecorder) PublishEventWithChangedFields(
	ctx context.Context, eventType datatypes.EventType, data any, changedFields []string,
) {
	r.Publish(ctx, service.NewEvent(eventType, data, changedFields))
}

func (r *eventRecorder) Publish(_ context.Context, event service.Event) {
	r.events = append(r.events, event)
}

// transactionalEnqueueEnv is one isolated tenant and pair of models in the shared test database.
type transactionalEnqueueEnv struct {
	db        *pgxpool.Pool
	tenantID  string
	rawModel  string
	taxModel  string
	providers []*service.EmbeddingProvider
}

func newTransactionalEnqueueEnv(t *testing.T) *transactionalEnqueueEnv {
	t.Helper()

	ctx := context.Background()
	db := newTenantLockDB(ctx, t)

	env := &transactionalEnqueueEnv{
		db:       db,
		tenantID: "embedding-tx-enqueue-" + uuid.NewString(),
		// Unique models isolate this test's jobs from every other job in the shared database.
		rawModel: "raw-" + uuid.NewString(),
		taxModel: "taxonomy:" + uuid.NewString() + ":translated-v1",
	}
	env.providers = []*service.EmbeddingProvider{
		service.NewEmbeddingProvider(env.rawModel, service.EmbeddingsQueueName, 5, "", 0, nil),
		service.NewEmbeddingProviderForInputKind(env.taxModel, service.EmbeddingsQueueName, 5, "", 0, nil,
			models.EmbeddingInputKindTaxonomyTranslated),
	}

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM river_job WHERE args->>'model' IN ($1, $2)`, env.rawModel, env.taxModel)
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, env.tenantID)
	})

	return env
}

// service builds a records service whose embedding jobs go through inserter.
func (e *transactionalEnqueueEnv) service(
	inserter service.RiverTxBatchInserter, publisher service.MessagePublisher,
) *service.FeedbackRecordsService {
	svc := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(e.db), nil, "", publisher, nil, "", 0, "en-US")
	svc.SetEmbeddingEnqueuer(service.NewEmbeddingEnqueuer(inserter, e.providers...))

	return svc
}

func (e *transactionalEnqueueEnv) riverClient(t *testing.T) *river.Client[pgx.Tx] {
	t.Helper()

	client, err := river.NewClient(riverpgxv5.New(e.db), &river.Config{})
	require.NoError(t, err)

	return client
}

func (e *transactionalEnqueueEnv) createRequest(valueText string) *models.CreateFeedbackRecordRequest {
	return &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeCategorical,
		ValueText:    &valueText,
		TenantID:     e.tenantID,
		SubmissionID: "submission-" + uuid.NewString(),
	}
}

// embeddingJob is the part of a queued job these tests assert on.
type embeddingJob struct {
	model   string
	eventID string
	hash    string
}

func (e *transactionalEnqueueEnv) jobs(t *testing.T, recordID uuid.UUID) []embeddingJob {
	t.Helper()

	rows, err := e.db.Query(context.Background(), `
		SELECT args->>'model', args->>'event_id', args->>'value_text_hash'
		FROM river_job
		WHERE kind = 'feedback_embedding' AND args->>'feedback_record_id' = $1
		ORDER BY id`, recordID.String())
	require.NoError(t, err)

	jobs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (embeddingJob, error) {
		var job embeddingJob

		return job, row.Scan(&job.model, &job.eventID, &job.hash)
	})
	require.NoError(t, err)

	return jobs
}

func (e *transactionalEnqueueEnv) recordsWithSubmission(t *testing.T, submissionID string) int {
	t.Helper()

	var count int
	require.NoError(t, e.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM feedback_records WHERE tenant_id = $1 AND submission_id = $2`,
		e.tenantID, submissionID).Scan(&count))

	return count
}

// TestEmbeddingJobsCommitWithTheRecordWrite proves the commit/enqueue boundary is atomic (ENG-2792):
// a write and the embedding jobs it owes commit together or not at all, so no crash point between
// them can leave a record that no job will ever embed. Before this, jobs were enqueued after the
// commit through an in-process channel; a hub-api SIGKILL after 400 acknowledged creates left 241
// of them without a job.
func TestEmbeddingJobsCommitWithTheRecordWrite(t *testing.T) {
	t.Run("create commits its jobs without the event pipeline", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)
		// No publisher: the jobs must not depend on the in-process event channel at all.
		svc := env.service(env.riverClient(t), nil)

		record, err := svc.CreateFeedbackRecord(t.Context(), env.createRequest("Very satisfied"))
		require.NoError(t, err)

		jobs := env.jobs(t, record.ID)
		require.Len(t, jobs, 2, "a create owes one job per model")
		assert.ElementsMatch(t, []string{env.rawModel, env.taxModel}, []string{jobs[0].model, jobs[1].model})
	})

	t.Run("jobs and published event share one event id", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)
		publisher := &eventRecorder{}
		svc := env.service(env.riverClient(t), publisher)

		record, err := svc.CreateFeedbackRecord(t.Context(), env.createRequest("Very satisfied"))
		require.NoError(t, err)

		require.Len(t, publisher.events, 1)
		eventID := publisher.events[0].ID.String()

		for _, job := range env.jobs(t, record.ID) {
			assert.Equal(t, eventID, job.eventID, "job %s must correlate with the published event", job.model)
		}
	})

	t.Run("failed enqueue rolls the create back", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)
		publisher := &eventRecorder{}
		svc := env.service(failingTxInserter{}, publisher)
		req := env.createRequest("Very satisfied")

		_, err := svc.CreateFeedbackRecord(t.Context(), req)
		require.ErrorIs(t, err, errEnqueueBoundary)

		assert.Zero(t, env.recordsWithSubmission(t, req.SubmissionID),
			"a record whose jobs could not be enqueued must not be committed")
		assert.Empty(t, publisher.events, "nothing is published for a write that did not commit")
	})

	t.Run("content edit commits its jobs; metadata-only edit owes none", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)
		svc := env.service(env.riverClient(t), nil)

		record, err := svc.CreateFeedbackRecord(t.Context(), env.createRequest("Very satisfied"))
		require.NoError(t, err)
		require.Len(t, env.jobs(t, record.ID), 2)

		metadata := []byte(`{"k":"v"}`)
		_, err = svc.UpdateFeedbackRecord(t.Context(), record.ID, &models.UpdateFeedbackRecordRequest{Metadata: metadata})
		require.NoError(t, err)
		assert.Len(t, env.jobs(t, record.ID), 2, "metadata does not feed any embedding input")

		same := "Very satisfied"
		_, err = svc.UpdateFeedbackRecord(t.Context(), record.ID, &models.UpdateFeedbackRecordRequest{ValueText: &same})
		require.NoError(t, err)
		assert.Len(t, env.jobs(t, record.ID), 2, "re-sending the same text changes nothing and owes nothing")

		changed := "Not satisfied"
		_, err = svc.UpdateFeedbackRecord(t.Context(), record.ID, &models.UpdateFeedbackRecordRequest{ValueText: &changed})
		require.NoError(t, err)
		assert.Len(t, env.jobs(t, record.ID), 4, "a content edit owes a re-embed per model")
	})

	t.Run("failed enqueue rolls the edit back", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)

		record, err := env.service(env.riverClient(t), nil).
			CreateFeedbackRecord(t.Context(), env.createRequest("Very satisfied"))
		require.NoError(t, err)

		changed := "Not satisfied"
		_, err = env.service(failingTxInserter{}, nil).
			UpdateFeedbackRecord(t.Context(), record.ID, &models.UpdateFeedbackRecordRequest{ValueText: &changed})
		require.ErrorIs(t, err, errEnqueueBoundary)

		current, err := repository.NewFeedbackRecordsRepository(env.db).GetByID(t.Context(), record.ID)
		require.NoError(t, err)
		require.NotNil(t, current.ValueText)
		assert.Equal(t, "Very satisfied", *current.ValueText,
			"an edit whose re-embed could not be enqueued must not be committed, or its old vector is stale forever")
	})

	t.Run("translation write commits its taxonomy re-embed", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)
		svc := env.service(env.riverClient(t), nil)

		record, err := svc.CreateFeedbackRecord(t.Context(), env.createRequest("Sehr zufrieden"))
		require.NoError(t, err)

		translated := "Very satisfied"
		require.NoError(t, svc.SetTranslation(t.Context(), record.ID, &translated, "en-US", nil))

		jobs := env.jobs(t, record.ID)
		require.Len(t, jobs, 3)
		assert.Equal(t, env.taxModel, jobs[2].model, "only the taxonomy input reads the translation")
		assert.Equal(t, "translation", jobs[2].hash)
	})

	t.Run("failed enqueue rolls the translation back", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)

		record, err := env.service(env.riverClient(t), nil).
			CreateFeedbackRecord(t.Context(), env.createRequest("Sehr zufrieden"))
		require.NoError(t, err)

		translated := "Very satisfied"
		err = env.service(failingTxInserter{}, nil).SetTranslation(t.Context(), record.ID, &translated, "en-US", nil)
		require.ErrorIs(t, err, errEnqueueBoundary)

		current, err := repository.NewFeedbackRecordsRepository(env.db).GetByID(t.Context(), record.ID)
		require.NoError(t, err)
		assert.Nil(t, current.ValueTextTranslated,
			"a translation whose taxonomy re-embed could not be enqueued must not be committed")
	})

	t.Run("superseded translation owes no re-embed", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)
		svc := env.service(env.riverClient(t), nil)

		record, err := svc.CreateFeedbackRecord(t.Context(), env.createRequest("Sehr zufrieden"))
		require.NoError(t, err)

		translated := "Very satisfied"
		staleText := func(*string) bool { return false }
		err = svc.SetTranslation(t.Context(), record.ID, &translated, "en-US", staleText)
		require.ErrorIs(t, err, huberrors.ErrTranslationSuperseded)
		assert.Len(t, env.jobs(t, record.ID), 2, "a skipped write owes nothing")
	})

	t.Run("tenant purge in progress rejects the create and its jobs", func(t *testing.T) {
		env := newTransactionalEnqueueEnv(t)
		svc := env.service(env.riverClient(t), nil)
		req := env.createRequest("Very satisfied")

		release := holdTenantWriteLock(context.Background(), t, env.db, env.tenantID, false)
		defer release()

		_, err := svc.CreateFeedbackRecord(t.Context(), req)
		require.ErrorIs(t, err, huberrors.ErrTenantWriteConflict)

		release()
		assert.Zero(t, env.recordsWithSubmission(t, req.SubmissionID))

		var jobs int
		require.NoError(t, env.db.QueryRow(t.Context(),
			`SELECT COUNT(*) FROM river_job WHERE args->>'model' IN ($1, $2)`, env.rawModel, env.taxModel).Scan(&jobs))
		assert.Zero(t, jobs)
	})
}
