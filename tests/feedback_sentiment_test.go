package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
	"github.com/formbricks/hub/pkg/database"
)

// TestFeedbackRecords_SentimentFields covers the ENG-1529 data-model step: sentiment /
// sentiment_score are NULL on a fresh record, round-trip through the read path once set
// (confirming the typed-enum scan), and the CHECK constraints reject an out-of-range score
// and an unknown label.
func TestFeedbackRecords_SentimentFields(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)

	valueText := "Great product"
	created, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &valueText,
		TenantID:     testTenantID("sentiment"),
		SubmissionID: testTenantID("submission"),
	})
	require.NoError(t, err)

	// Fresh record: both enrichment fields are NULL until a record is enriched.
	require.Nil(t, created.Sentiment)
	require.Nil(t, created.SentimentScore)

	// A valid label + score round-trips through the read path (scan into the typed enum).
	_, err = db.Exec(ctx,
		`UPDATE feedback_records SET sentiment = $2, sentiment_score = $3 WHERE id = $1`,
		created.ID, models.SentimentPositive, 0.5)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Sentiment)
	assert.Equal(t, models.SentimentPositive, *got.Sentiment)
	require.NotNil(t, got.SentimentScore)
	assert.InDelta(t, 0.5, *got.SentimentScore, 1e-9)

	// CHECK constraints reject an out-of-range score and an unknown label.
	_, err = db.Exec(ctx, `UPDATE feedback_records SET sentiment_score = 3.0 WHERE id = $1`, created.ID)
	require.Error(t, err, "score > 1 must violate feedback_records_sentiment_score_range")

	_, err = db.Exec(ctx, `UPDATE feedback_records SET sentiment = 'bogus' WHERE id = $1`, created.ID)
	require.Error(t, err, "unknown label must violate feedback_records_sentiment_valid")
}

// fakeSentimentClient is a service.SentimentClient stub returning a canned result/error and
// counting calls.
type fakeSentimentClient struct {
	result service.SentimentResult
	err    error
	calls  int
}

func (f *fakeSentimentClient) Classify(_ context.Context, _, _ string) (service.SentimentResult, error) {
	f.calls++

	return f.result, f.err
}

func sentimentWorkerJob(recordID uuid.UUID) *river.Job[service.FeedbackSentimentArgs] {
	return &river.Job[service.FeedbackSentimentArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3},
		Args:   service.FeedbackSentimentArgs{FeedbackRecordID: recordID, ValueTextHash: "h"},
	}
}

// TestFeedbackSentiment_WorkerPipeline drives FeedbackSentimentWorker end to end against
// Postgres (with a fake classifier): it classifies and persists the label+score through the
// real SetSentiment write, clears a stale sentiment when value_text is empty (without calling
// the provider), and SetSentiment on a missing record returns NotFound.
func TestFeedbackSentiment_WorkerPipeline(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	svc := service.NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	createText := func(valueText string) *models.FeedbackRecord {
		rec, createErr := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     testTenantID("sentiment-worker"),
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, createErr)

		return rec
	}

	t.Run("classifies and persists", func(t *testing.T) {
		rec := createText("Great product")
		fake := &fakeSentimentClient{result: service.SentimentResult{Label: models.SentimentPositive, Score: 0.5}}
		worker := workers.NewFeedbackSentimentWorker(svc, fake, nil)

		require.NoError(t, worker.Work(ctx, sentimentWorkerJob(rec.ID)))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.Sentiment)
		assert.Equal(t, models.SentimentPositive, *got.Sentiment)
		require.NotNil(t, got.SentimentScore)
		assert.InDelta(t, 0.5, *got.SentimentScore, 1e-9)
		assert.Equal(t, 1, fake.calls)
	})

	t.Run("clears a stale sentiment when value_text is empty", func(t *testing.T) {
		rec := createText("")

		// Seed a stale sentiment to prove the worker nulls it instead of classifying empty text.
		label := models.SentimentNegative
		score := -1.0
		require.NoError(t, svc.SetSentiment(ctx, rec.ID, &label, &score))

		fake := &fakeSentimentClient{result: service.SentimentResult{Label: models.SentimentPositive, Score: 1}}
		worker := workers.NewFeedbackSentimentWorker(svc, fake, nil)

		require.NoError(t, worker.Work(ctx, sentimentWorkerJob(rec.ID)))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.Sentiment, "empty value_text clears the sentiment")
		assert.Nil(t, got.SentimentScore)
		assert.Equal(t, 0, fake.calls, "empty text is not sent to the provider")
	})

	t.Run("worker skips a record gone before classify", func(t *testing.T) {
		fake := &fakeSentimentClient{result: service.SentimentResult{Label: models.SentimentPositive, Score: 1}}
		worker := workers.NewFeedbackSentimentWorker(svc, fake, nil)

		// A job for a nonexistent record: GetFeedbackRecord returns NotFound end to end
		// (service -> repo -> DB), which the worker treats as a benign skip — no error, no
		// classify call. This exercises the worker's not-found path through the real stack.
		require.NoError(t, worker.Work(ctx, sentimentWorkerJob(uuid.Must(uuid.NewV7()))))
		assert.Equal(t, 0, fake.calls, "a gone record is not classified")
	})

	t.Run("SetSentiment on a missing record returns NotFound", func(t *testing.T) {
		err := repo.SetSentiment(ctx, uuid.Must(uuid.NewV7()), nil, nil)
		require.ErrorIs(t, err, huberrors.ErrNotFound)
	})
}
