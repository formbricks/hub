package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

type mockEmbeddingClient struct {
	createFunc func(ctx context.Context, input string) ([]float32, error)
}

func (m *mockEmbeddingClient) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, input)
	}

	return []float32{0.1}, nil
}

type mockEmbeddingsRepoForSearch struct {
	getEmbeddingFunc func(ctx context.Context, feedbackRecordID uuid.UUID, model string) ([]float32, error)
	nearestFunc      func(
		ctx context.Context, model string, queryEmbedding []float32,
		tenantID string, limit int, excludeID *uuid.UUID, minScore float64,
	) ([]models.FeedbackRecordWithScore, error)
}

func (m *mockEmbeddingsRepoForSearch) GetEmbeddingByFeedbackRecordAndModel(
	ctx context.Context, feedbackRecordID uuid.UUID, model string,
) ([]float32, error) {
	if m.getEmbeddingFunc != nil {
		return m.getEmbeddingFunc(ctx, feedbackRecordID, model)
	}

	return nil, nil
}

func (m *mockEmbeddingsRepoForSearch) NearestFeedbackRecordsByEmbedding(
	ctx context.Context, model string, queryEmbedding []float32, tenantID string, limit int, excludeID *uuid.UUID, minScore float64,
) ([]models.FeedbackRecordWithScore, error) {
	if m.nearestFunc != nil {
		return m.nearestFunc(ctx, model, queryEmbedding, tenantID, limit, excludeID, minScore)
	}

	return nil, nil
}

func TestSearchService_SemanticSearch(t *testing.T) {
	t.Run("missing tenantID returns ErrMissingTenantID", func(t *testing.T) {
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo:  &mockEmbeddingsRepoForSearch{},
			Model:           "test-model",
			MinScore:        0.5,
		})
		results, err := svc.SemanticSearch(context.Background(), "query", "", 10)
		assert.Nil(t, results)
		assert.ErrorIs(t, err, ErrMissingTenantID)
	})

	t.Run("empty query returns ErrEmptyQuery", func(t *testing.T) {
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo:  &mockEmbeddingsRepoForSearch{},
			Model:           "test-model",
			MinScore:        0.5,
		})
		results, err := svc.SemanticSearch(context.Background(), "  ", "tenant-1", 10)
		assert.Nil(t, results)
		assert.ErrorIs(t, err, ErrEmptyQuery)
	})

	t.Run("success returns results from repo", func(t *testing.T) {
		id := uuid.MustParse("018e1234-5678-9abc-def0-111111111111")
		clientCalled := false
		nearestCalled := false
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{
				createFunc: func(_ context.Context, input string) ([]float32, error) {
					clientCalled = true

					assert.Equal(t, "login slow", input)

					return []float32{0.1, 0.2}, nil
				},
			},
			EmbeddingsRepo: &mockEmbeddingsRepoForSearch{
				nearestFunc: func(
					_ context.Context, model string, queryEmbedding []float32,
					tenantID string, limit int, excludeID *uuid.UUID, minScore float64,
				) ([]models.FeedbackRecordWithScore, error) {
					nearestCalled = true

					assert.Equal(t, "test-model", model)
					assert.Equal(t, []float32{0.1, 0.2}, queryEmbedding)
					assert.Equal(t, "env-1", tenantID)
					assert.Equal(t, 10, limit)
					assert.Nil(t, excludeID)
					assert.InDelta(t, 0.5, minScore, 1e-9)

					return []models.FeedbackRecordWithScore{
						{FeedbackRecordID: id, Score: 0.91},
					}, nil
				},
			},
			Model:    "test-model",
			MinScore: 0.5,
		})
		results, err := svc.SemanticSearch(context.Background(), "login slow", "env-1", 10)
		require.NoError(t, err)
		require.True(t, clientCalled)
		require.True(t, nearestCalled)
		require.Len(t, results, 1)
		assert.Equal(t, id, results[0].FeedbackRecordID)
		assert.InDelta(t, 0.91, results[0].Score, 1e-9)
	})
}

func TestSearchService_SimilarFeedback(t *testing.T) {
	t.Run("missing tenantID returns ErrMissingTenantID", func(t *testing.T) {
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo:  &mockEmbeddingsRepoForSearch{},
			Model:           "test-model",
			MinScore:        0.5,
		})
		results, err := svc.SimilarFeedback(context.Background(), uuid.MustParse("018e1234-5678-9abc-def0-123456789abc"), "", 10)
		assert.Nil(t, results)
		assert.ErrorIs(t, err, ErrMissingTenantID)
	})

	t.Run("embedding not found returns ErrEmbeddingNotFound", func(t *testing.T) {
		rid := uuid.MustParse("018e1234-5678-9abc-def0-123456789abc")
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo: &mockEmbeddingsRepoForSearch{
				getEmbeddingFunc: func(_ context.Context, id uuid.UUID, _ string) ([]float32, error) {
					assert.Equal(t, rid, id)

					return nil, repository.ErrEmbeddingNotFound
				},
			},
			Model:    "test-model",
			MinScore: 0.5,
		})
		results, err := svc.SimilarFeedback(context.Background(), rid, "env-1", 10)
		assert.Nil(t, results)
		assert.ErrorIs(t, err, repository.ErrEmbeddingNotFound)
	})

	t.Run("success returns results and excludes source record", func(t *testing.T) {
		sourceID := uuid.MustParse("018e1234-5678-9abc-def0-123456789abc")
		similarID := uuid.MustParse("018e1234-5678-9abc-def0-aaaaaaaaaaaa")
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo: &mockEmbeddingsRepoForSearch{
				getEmbeddingFunc: func(_ context.Context, id uuid.UUID, model string) ([]float32, error) {
					assert.Equal(t, sourceID, id)
					assert.Equal(t, "test-model", model)

					return []float32{0.1, 0.2}, nil
				},
				nearestFunc: func(
					_ context.Context, model string, _ []float32,
					tenantID string, limit int, excludeID *uuid.UUID, minScore float64,
				) ([]models.FeedbackRecordWithScore, error) {
					assert.Equal(t, "test-model", model)
					assert.Equal(t, "env-1", tenantID)
					assert.Equal(t, 10, limit)
					require.NotNil(t, excludeID)
					assert.Equal(t, sourceID, *excludeID)
					assert.InDelta(t, 0.5, minScore, 1e-9)

					return []models.FeedbackRecordWithScore{
						{FeedbackRecordID: similarID, Score: 0.88},
					}, nil
				},
			},
			Model:    "test-model",
			MinScore: 0.5,
		})
		results, err := svc.SimilarFeedback(context.Background(), sourceID, "env-1", 10)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, similarID, results[0].FeedbackRecordID)
		assert.InDelta(t, 0.88, results[0].Score, 1e-9)
	})
}

func TestSearchService_SemanticSearch_EmbeddingError(t *testing.T) {
	embeddingErr := errors.New("embedding API failed")
	svc := NewSearchService(SearchServiceParams{
		EmbeddingClient: &mockEmbeddingClient{
			createFunc: func(_ context.Context, _ string) ([]float32, error) {
				return nil, embeddingErr
			},
		},
		EmbeddingsRepo: &mockEmbeddingsRepoForSearch{},
		Model:          "test-model",
		MinScore:       0.5,
	})
	results, err := svc.SemanticSearch(context.Background(), "query", "env-1", 10)
	assert.Nil(t, results)
	assert.ErrorIs(t, err, embeddingErr)
}
