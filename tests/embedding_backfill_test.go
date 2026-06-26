package tests

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

// countingEmbeddingInserter records the FeedbackEmbeddingArgs jobs enqueued.
type countingEmbeddingInserter struct {
	ids []uuid.UUID
}

func (c *countingEmbeddingInserter) Insert(
	_ context.Context, args river.JobArgs, _ *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	if a, ok := args.(service.FeedbackEmbeddingArgs); ok {
		c.ids = append(c.ids, a.FeedbackRecordID)
	}

	return &rivertype.JobInsertResult{}, nil
}

func embeddingBackfillRepos(t *testing.T) (*repository.FeedbackRecordsRepository, *repository.EmbeddingsRepository) {
	t.Helper()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(
		context.Background(), cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	t.Cleanup(db.Close)

	return repository.NewFeedbackRecordsRepository(db), repository.NewEmbeddingsRepository(db)
}

// TestListFeedbackRecordIDsForBackfill_KeysetPagination locks the new keyset behavior of the
// embedding backfill query: each page is bounded by the limit, ids are returned strictly
// ascending with no id on two pages, and paging from uuid.Nil eventually returns every record
// that needs an embedding. A fresh model is used so the created records are all eligible.
func TestListFeedbackRecordIDsForBackfill_KeysetPagination(t *testing.T) {
	ctx := context.Background()
	feedbackRepo, embeddingsRepo := embeddingBackfillRepos(t)

	model := "backfill-keyset-" + uuid.NewString()
	tenant := uuid.NewString()

	makeText := func(value string) uuid.UUID {
		rec, err := feedbackRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			SubmissionID: uuid.NewString(),
			TenantID:     tenant,
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &value,
		})
		require.NoError(t, err)

		return rec.ID
	}

	mine := map[uuid.UUID]bool{}
	for _, value := range []string{"one", "two", "three", "four", "five"} {
		mine[makeText(value)] = true
	}

	seen := map[uuid.UUID]bool{}
	afterID := uuid.Nil

	var prev uuid.UUID

	hasPrev := false

	for {
		page, err := embeddingsRepo.ListFeedbackRecordIDsForBackfill(ctx, model, afterID, 2)
		require.NoError(t, err)
		require.LessOrEqual(t, len(page), 2, "LIMIT bounds the page size")

		if len(page) == 0 {
			break
		}

		for _, id := range page {
			require.False(t, seen[id], "an id must not appear on two pages")
			seen[id] = true

			if hasPrev {
				require.Negativef(t, bytes.Compare(prev[:], id[:]), "ids are returned strictly ascending")
			}

			prev = id
			hasPrev = true
		}

		afterID = page[len(page)-1]

		if len(page) < 2 {
			break
		}
	}

	for id := range mine {
		assert.Truef(t, seen[id], "record %s needing an embedding must be returned across pages", id)
	}
}

// TestBackfillEmbeddings_StreamsAllEligible drives the service backfill against the real DB
// with a recording inserter and asserts it enqueues an embedding job for every record that
// needs one (a fresh model makes the created records eligible).
func TestBackfillEmbeddings_StreamsAllEligible(t *testing.T) {
	ctx := context.Background()
	feedbackRepo, embeddingsRepo := embeddingBackfillRepos(t)

	model := "backfill-stream-" + uuid.NewString()
	tenant := uuid.NewString()

	makeText := func(value string) uuid.UUID {
		rec, err := feedbackRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			SubmissionID: uuid.NewString(),
			TenantID:     tenant,
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &value,
		})
		require.NoError(t, err)

		return rec.ID
	}

	mine := []uuid.UUID{makeText("one"), makeText("two"), makeText("three")}

	inserter := &countingEmbeddingInserter{}
	svc := service.NewFeedbackRecordsService(feedbackRepo, embeddingsRepo, model, nil, inserter, "embeddings", 3, "")

	enqueued, err := svc.BackfillEmbeddings(ctx, model)
	require.NoError(t, err)
	require.GreaterOrEqual(t, enqueued, len(mine))

	got := map[uuid.UUID]bool{}
	for _, id := range inserter.ids {
		got[id] = true
	}

	for _, id := range mine {
		assert.Truef(t, got[id], "record %s needing an embedding must be enqueued", id)
	}
}
