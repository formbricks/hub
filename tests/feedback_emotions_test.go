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

// TestFeedbackRecords_EmotionsField covers the ENG-1573 data-model step: emotions is NULL on a
// fresh record, a text[] round-trips through the read path once set (confirming the []string scan
// maps to []EmotionValue), and the CHECK constraints reject an out-of-pool label and the empty
// array (absence is NULL, never {}).
func TestFeedbackRecords_EmotionsField(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)

	valueText := "I am thrilled and a little scared"
	created, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &valueText,
		TenantID:     testTenantID("emotions"),
		SubmissionID: testTenantID("submission"),
	})
	require.NoError(t, err)

	// Fresh record: emotions is NULL until enriched.
	require.Nil(t, created.Emotions)

	// A valid multi-label array round-trips through the read path (text[] scanned to []EmotionValue).
	_, err = db.Exec(ctx,
		`UPDATE feedback_records SET emotions = $2 WHERE id = $1`,
		created.ID, []string{"joy", "fear"})
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Emotions)
	assert.Equal(t, []models.EmotionValue{models.EmotionJoy, models.EmotionFear}, *got.Emotions)

	// CHECK constraints reject an out-of-pool label and the empty array.
	_, err = db.Exec(ctx, `UPDATE feedback_records SET emotions = ARRAY['bogus']::text[] WHERE id = $1`, created.ID)
	require.Error(t, err, "an out-of-pool label must violate feedback_records_emotions_valid")

	_, err = db.Exec(ctx, `UPDATE feedback_records SET emotions = '{}'::text[] WHERE id = $1`, created.ID)
	require.Error(t, err, "the empty array must violate feedback_records_emotions_non_empty")
}

// fakeEmotionsClient is a service.EmotionsClient stub returning a canned result/error and counting
// calls.
type fakeEmotionsClient struct {
	result service.EmotionsResult
	err    error
	calls  int
}

func (f *fakeEmotionsClient) Classify(_ context.Context, _, _ string) (service.EmotionsResult, error) {
	f.calls++

	return f.result, f.err
}

func emotionsWorkerJob(recordID uuid.UUID) *river.Job[service.FeedbackEmotionsArgs] {
	return &river.Job[service.FeedbackEmotionsArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3},
		Args:   service.FeedbackEmotionsArgs{FeedbackRecordID: recordID, ValueTextHash: "h"},
	}
}

// TestFeedbackEmotions_WorkerPipeline drives FeedbackEmotionsWorker end to end against Postgres
// (with a fake classifier): it classifies and persists labels through the real SetEmotions write,
// clears when the classifier returns no emotions, clears a stale set when value_text is empty
// (without calling the provider), and SetEmotions on a missing record returns NotFound.
func TestFeedbackEmotions_WorkerPipeline(t *testing.T) {
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
			TenantID:     testTenantID("emotions-worker"),
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, createErr)

		return rec
	}

	t.Run("classifies and persists", func(t *testing.T) {
		rec := createText("I am thrilled and a little scared")
		fake := &fakeEmotionsClient{result: service.EmotionsResult{
			Labels: []models.EmotionValue{models.EmotionJoy, models.EmotionFear},
		}}
		worker := workers.NewFeedbackEmotionsWorker(svc, fake, nil)

		require.NoError(t, worker.Work(ctx, emotionsWorkerJob(rec.ID)))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.Emotions)
		assert.Equal(t, []models.EmotionValue{models.EmotionJoy, models.EmotionFear}, *got.Emotions)
		assert.Equal(t, 1, fake.calls)
	})

	t.Run("clears when the classifier returns no emotions", func(t *testing.T) {
		rec := createText("the weather forecast for tomorrow")

		// Seed a stale set to prove an empty classification nulls the column rather than leaving it.
		require.NoError(t, svc.SetEmotions(ctx, rec.ID, []models.EmotionValue{models.EmotionAnger}))

		fake := &fakeEmotionsClient{result: service.EmotionsResult{Labels: nil}}
		worker := workers.NewFeedbackEmotionsWorker(svc, fake, nil)

		require.NoError(t, worker.Work(ctx, emotionsWorkerJob(rec.ID)))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.Emotions, "no emotion detected clears the column")
		assert.Equal(t, 1, fake.calls)
	})

	t.Run("clears a stale set when value_text is empty", func(t *testing.T) {
		rec := createText("")

		require.NoError(t, svc.SetEmotions(ctx, rec.ID, []models.EmotionValue{models.EmotionSadness}))

		fake := &fakeEmotionsClient{result: service.EmotionsResult{Labels: []models.EmotionValue{models.EmotionJoy}}}
		worker := workers.NewFeedbackEmotionsWorker(svc, fake, nil)

		require.NoError(t, worker.Work(ctx, emotionsWorkerJob(rec.ID)))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.Emotions, "empty value_text clears the emotions")
		assert.Equal(t, 0, fake.calls, "empty text is not sent to the provider")
	})

	t.Run("worker skips a record gone before classify", func(t *testing.T) {
		fake := &fakeEmotionsClient{result: service.EmotionsResult{Labels: []models.EmotionValue{models.EmotionJoy}}}
		worker := workers.NewFeedbackEmotionsWorker(svc, fake, nil)

		require.NoError(t, worker.Work(ctx, emotionsWorkerJob(uuid.Must(uuid.NewV7()))))
		assert.Equal(t, 0, fake.calls, "a gone record is not classified")
	})

	t.Run("SetEmotions on a missing record returns NotFound", func(t *testing.T) {
		err := repo.SetEmotions(ctx, uuid.Must(uuid.NewV7()), nil)
		require.ErrorIs(t, err, huberrors.ErrNotFound)
	})
}
