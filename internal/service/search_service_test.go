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
	createFunc      func(ctx context.Context, input string) ([]float32, error)
	createQueryFunc func(ctx context.Context, input string) ([]float32, error)
}

func (m *mockEmbeddingClient) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, input)
	}

	return []float32{0.1}, nil
}

func (m *mockEmbeddingClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	if m.createQueryFunc != nil {
		return m.createQueryFunc(ctx, input)
	}

	return m.CreateEmbedding(ctx, input)
}

type mockEmbeddingsRepoForSearch struct {
	getEmbeddingByTenantFunc func(ctx context.Context, feedbackRecordID uuid.UUID, model, tenantID string) ([]float32, error)
	nearestFunc              func(
		ctx context.Context, model string, queryEmbedding []float32,
		tenantID string, limit, offset int, excludeID *uuid.UUID, minScore float64,
	) ([]models.FeedbackRecordWithScore, error)
	nearestAfterFunc func(
		ctx context.Context, model string, queryEmbedding []float32,
		tenantID string, limit int, lastDistance float64, lastID uuid.UUID, excludeID *uuid.UUID, minScore float64,
	) ([]models.FeedbackRecordWithScore, error)
}

func (m *mockEmbeddingsRepoForSearch) GetEmbeddingByFeedbackRecordAndModelAndTenant(
	ctx context.Context, feedbackRecordID uuid.UUID, model, tenantID string,
) ([]float32, error) {
	if m.getEmbeddingByTenantFunc != nil {
		return m.getEmbeddingByTenantFunc(ctx, feedbackRecordID, model, tenantID)
	}

	return nil, repository.ErrEmbeddingNotFound
}

func (m *mockEmbeddingsRepoForSearch) NearestFeedbackRecordsByEmbedding(
	ctx context.Context, model string, queryEmbedding []float32, tenantID string, limit, offset int, excludeID *uuid.UUID, minScore float64,
) ([]models.FeedbackRecordWithScore, error) {
	if m.nearestFunc != nil {
		return m.nearestFunc(ctx, model, queryEmbedding, tenantID, limit, offset, excludeID, minScore)
	}

	return nil, nil
}

func (m *mockEmbeddingsRepoForSearch) NearestFeedbackRecordsByEmbeddingAfterCursor(
	ctx context.Context, model string, queryEmbedding []float32, tenantID string, limit int,
	lastDistance float64, lastFeedbackRecordID uuid.UUID, excludeID *uuid.UUID, minScore float64,
) ([]models.FeedbackRecordWithScore, error) {
	if m.nearestAfterFunc != nil {
		return m.nearestAfterFunc(ctx, model, queryEmbedding, tenantID, limit, lastDistance, lastFeedbackRecordID, excludeID, minScore)
	}

	return nil, nil
}

func TestSearchService_SemanticSearch(t *testing.T) {
	t.Run("missing tenantID returns ErrMissingTenantID", func(t *testing.T) {
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo:  &mockEmbeddingsRepoForSearch{},
			Model:           "test-model",
		})
		res, err := svc.SemanticSearch(context.Background(), "query", "", 10, 0, 0, "")
		assert.Empty(t, res.Results)
		assert.ErrorIs(t, err, ErrMissingTenantID)
	})

	t.Run("empty query returns ErrEmptyQuery", func(t *testing.T) {
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo:  &mockEmbeddingsRepoForSearch{},
			Model:           "test-model",
		})
		res, err := svc.SemanticSearch(context.Background(), "  ", "tenant-1", 10, 0, 0, "")
		assert.Empty(t, res.Results)
		assert.ErrorIs(t, err, ErrEmptyQuery)
	})

	t.Run("success returns results from repo", func(t *testing.T) {
		id := uuid.MustParse("018e1234-5678-9abc-def0-111111111111")
		queryClientCalled := false
		nearestCalled := false
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{
				createFunc: func(_ context.Context, _ string) ([]float32, error) {
					t.Fatal("semantic search must use CreateEmbeddingForQuery, not CreateEmbedding")

					return nil, nil
				},
				createQueryFunc: func(_ context.Context, input string) ([]float32, error) {
					queryClientCalled = true

					assert.Equal(t, "login slow", input)

					return []float32{0.1, 0.2}, nil
				},
			},
			EmbeddingsRepo: &mockEmbeddingsRepoForSearch{
				nearestFunc: func(
					_ context.Context, model string, queryEmbedding []float32,
					tenantID string, limit, offset int, excludeID *uuid.UUID, minScore float64,
				) ([]models.FeedbackRecordWithScore, error) {
					nearestCalled = true

					assert.Equal(t, "test-model", model)
					assert.Equal(t, []float32{0.1, 0.2}, queryEmbedding)
					assert.Equal(t, "env-1", tenantID)
					assert.Equal(t, 10, limit)
					assert.Equal(t, 2, offset)
					assert.Nil(t, excludeID)
					assert.InDelta(t, 0.5, minScore, 1e-9)

					return []models.FeedbackRecordWithScore{
						{FeedbackRecordID: id, Score: 0.91, FieldLabel: "", ValueText: ""},
					}, nil
				},
			},
			Model: "test-model",
		})
		res, err := svc.SemanticSearch(context.Background(), "login slow", "env-1", 10, 2, 0.5, "")
		require.NoError(t, err)
		require.True(t, queryClientCalled)
		require.True(t, nearestCalled)
		require.Len(t, res.Results, 1)
		assert.Equal(t, id, res.Results[0].FeedbackRecordID)
		assert.InDelta(t, 0.91, res.Results[0].Score, 1e-9)
	})
}

func TestSearchService_SimilarFeedback(t *testing.T) {
	t.Run("missing tenantID returns ErrMissingTenantID", func(t *testing.T) {
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo:  &mockEmbeddingsRepoForSearch{},
			Model:           "test-model",
		})
		res, err := svc.SimilarFeedback(context.Background(), uuid.MustParse("018e1234-5678-9abc-def0-123456789abc"), "", 10, 0, 0, "")
		assert.Empty(t, res.Results)
		assert.ErrorIs(t, err, ErrMissingTenantID)
	})

	t.Run("embedding not found returns ErrEmbeddingNotFound", func(t *testing.T) {
		rid := uuid.MustParse("018e1234-5678-9abc-def0-123456789abc")
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo: &mockEmbeddingsRepoForSearch{
				getEmbeddingByTenantFunc: func(_ context.Context, id uuid.UUID, _, tenantID string) ([]float32, error) {
					assert.Equal(t, rid, id)
					assert.Equal(t, "env-1", tenantID)

					return nil, repository.ErrEmbeddingNotFound
				},
			},
			Model: "test-model",
		})
		res, err := svc.SimilarFeedback(context.Background(), rid, "env-1", 10, 0, 0, "")
		assert.Empty(t, res.Results)
		assert.ErrorIs(t, err, repository.ErrEmbeddingNotFound)
	})

	t.Run("success returns results and excludes source record", func(t *testing.T) {
		sourceID := uuid.MustParse("018e1234-5678-9abc-def0-123456789abc")
		similarID := uuid.MustParse("018e1234-5678-9abc-def0-aaaaaaaaaaaa")
		svc := NewSearchService(SearchServiceParams{
			EmbeddingClient: &mockEmbeddingClient{},
			EmbeddingsRepo: &mockEmbeddingsRepoForSearch{
				getEmbeddingByTenantFunc: func(_ context.Context, id uuid.UUID, model, tenantID string) ([]float32, error) {
					assert.Equal(t, sourceID, id)
					assert.Equal(t, "test-model", model)
					assert.Equal(t, "env-1", tenantID)

					return []float32{0.1, 0.2}, nil
				},
				nearestFunc: func(
					_ context.Context, model string, _ []float32,
					tenantID string, limit, offset int, excludeID *uuid.UUID, minScore float64,
				) ([]models.FeedbackRecordWithScore, error) {
					assert.Equal(t, "test-model", model)
					assert.Equal(t, "env-1", tenantID)
					assert.Equal(t, 10, limit)
					assert.Equal(t, 0, offset)
					require.NotNil(t, excludeID)
					assert.Equal(t, sourceID, *excludeID)
					assert.InDelta(t, 0.5, minScore, 1e-9)

					return []models.FeedbackRecordWithScore{
						{FeedbackRecordID: similarID, Score: 0.88, FieldLabel: "", ValueText: ""},
					}, nil
				},
			},
			Model: "test-model",
		})
		res, err := svc.SimilarFeedback(context.Background(), sourceID, "env-1", 10, 0, 0.5, "")
		require.NoError(t, err)
		require.Len(t, res.Results, 1)
		assert.Equal(t, similarID, res.Results[0].FeedbackRecordID)
		assert.InDelta(t, 0.88, res.Results[0].Score, 1e-9)
	})
}

func TestSearchService_SemanticSearch_EmbeddingError(t *testing.T) {
	embeddingErr := errors.New("embedding API failed")
	svc := NewSearchService(SearchServiceParams{
		EmbeddingClient: &mockEmbeddingClient{
			createFunc: func(_ context.Context, _ string) ([]float32, error) {
				t.Fatal("semantic search must use CreateEmbeddingForQuery, not CreateEmbedding")

				return nil, nil
			},
			createQueryFunc: func(_ context.Context, _ string) ([]float32, error) {
				return nil, embeddingErr
			},
		},
		EmbeddingsRepo: &mockEmbeddingsRepoForSearch{},
		Model:          "test-model",
	})
	res, err := svc.SemanticSearch(context.Background(), "query", "env-1", 10, 0, 0, "")
	assert.Empty(t, res.Results)
	assert.ErrorIs(t, err, embeddingErr)
}
