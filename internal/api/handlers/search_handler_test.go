package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

type mockSearchService struct {
	semanticFunc func(ctx context.Context, query, tenantID string, topK int) ([]models.FeedbackRecordWithScore, error)
	similarFunc  func(ctx context.Context, feedbackRecordID uuid.UUID, tenantID string, limit int) ([]models.FeedbackRecordWithScore, error)
}

func (m *mockSearchService) SemanticSearch(
	ctx context.Context, query, tenantID string, topK int,
) ([]models.FeedbackRecordWithScore, error) {
	if m.semanticFunc != nil {
		return m.semanticFunc(ctx, query, tenantID, topK)
	}

	return nil, nil
}

func (m *mockSearchService) SimilarFeedback(
	ctx context.Context, feedbackRecordID uuid.UUID, tenantID string, limit int,
) ([]models.FeedbackRecordWithScore, error) {
	if m.similarFunc != nil {
		return m.similarFunc(ctx, feedbackRecordID, tenantID, limit)
	}

	return nil, nil
}

func TestSearchHandler_SemanticSearch(t *testing.T) {
	t.Run("missing tenantId returns 400", func(t *testing.T) {
		handler := NewSearchHandler(&mockSearchService{})
		body := []byte(`{"query":"login is slow","topK":10}`)
		req := httptest.NewRequest(http.MethodPost, "http://test/v1/feedback-records/search/semantic", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()

		handler.SemanticSearch(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("empty query returns 400", func(t *testing.T) {
		called := false
		mock := &mockSearchService{
			semanticFunc: func(_ context.Context, _, _ string, _ int) ([]models.FeedbackRecordWithScore, error) {
				called = true

				return nil, service.ErrEmptyQuery
			},
		}
		handler := NewSearchHandler(mock)
		body := []byte(`{"query":"  ","topK":10,"tenantId":"env-1"}`)
		req := httptest.NewRequest(http.MethodPost, "http://test/v1/feedback-records/search/semantic", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()

		handler.SemanticSearch(rec, req)

		require.True(t, called)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("success returns 200 with results and value", func(t *testing.T) {
		id1 := uuid.MustParse("018e1234-5678-9abc-def0-111111111111")
		id2 := uuid.MustParse("018e1234-5678-9abc-def0-222222222222")
		val1 := "Login is very slow."
		val2 := "Dashboard loads fast."
		mock := &mockSearchService{
			semanticFunc: func(_ context.Context, query, tenantID string, topK int) ([]models.FeedbackRecordWithScore, error) {
				assert.Equal(t, "login is slow", query)
				assert.Equal(t, "env-1", tenantID)
				assert.Equal(t, 10, topK)

				return []models.FeedbackRecordWithScore{
					{FeedbackRecordID: id1, Score: 0.91, ValueText: val1},
					{FeedbackRecordID: id2, Score: 0.85, ValueText: val2},
				}, nil
			},
		}
		handler := NewSearchHandler(mock)
		body := []byte(`{"query":"login is slow","topK":10,"tenantId":"env-1"}`)
		req := httptest.NewRequest(http.MethodPost, "http://test/v1/feedback-records/search/semantic", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()

		handler.SemanticSearch(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp SemanticSearchResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		require.Len(t, resp.Results, 2)
		assert.Equal(t, id1, resp.Results[0].FeedbackRecordID)
		assert.InDelta(t, 0.91, resp.Results[0].Score, 1e-9)
		assert.Equal(t, val1, resp.Results[0].Value)
		assert.Equal(t, id2, resp.Results[1].FeedbackRecordID)
		assert.InDelta(t, 0.85, resp.Results[1].Score, 1e-9)
		assert.Equal(t, val2, resp.Results[1].Value)
	})
}

const similarURL = "http://test/v1/feedback-records/018e1234-5678-9abc-def0-123456789abc/similar"

func TestSearchHandler_SimilarFeedback(t *testing.T) {
	t.Run("missing tenantId returns 400", func(t *testing.T) {
		handler := NewSearchHandler(&mockSearchService{})
		req := httptest.NewRequest(http.MethodGet, similarURL, nil)
		rec := httptest.NewRecorder()

		req.SetPathValue("id", "018e1234-5678-9abc-def0-123456789abc")

		handler.SimilarFeedback(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("embedding not found returns 404", func(t *testing.T) {
		mock := &mockSearchService{
			similarFunc: func(_ context.Context, _ uuid.UUID, _ string, _ int) ([]models.FeedbackRecordWithScore, error) {
				return nil, service.ErrEmbeddingNotFound
			},
		}
		handler := NewSearchHandler(mock)
		req := httptest.NewRequest(http.MethodGet, similarURL+"?tenantId=env-1", nil)
		req.SetPathValue("id", "018e1234-5678-9abc-def0-123456789abc")

		rec := httptest.NewRecorder()

		handler.SimilarFeedback(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("success returns 200 with results and value", func(t *testing.T) {
		id := uuid.MustParse("018e1234-5678-9abc-def0-123456789abc")
		similarID := uuid.MustParse("018e1234-5678-9abc-def0-aaaaaaaaaaaa")
		similarVal := "Similar feedback text."
		mock := &mockSearchService{
			similarFunc: func(_ context.Context, fid uuid.UUID, tenantID string, limit int) ([]models.FeedbackRecordWithScore, error) {
				assert.Equal(t, id, fid)
				assert.Equal(t, "env-1", tenantID)
				assert.Equal(t, 10, limit)

				return []models.FeedbackRecordWithScore{
					{FeedbackRecordID: similarID, Score: 0.88, ValueText: similarVal},
				}, nil
			},
		}
		handler := NewSearchHandler(mock)
		req := httptest.NewRequest(http.MethodGet, similarURL+"?tenantId=env-1&topK=10", nil)
		req.SetPathValue("id", id.String())

		rec := httptest.NewRecorder()

		handler.SimilarFeedback(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp SemanticSearchResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		require.Len(t, resp.Results, 1)
		assert.Equal(t, similarID, resp.Results[0].FeedbackRecordID)
		assert.InDelta(t, 0.88, resp.Results[0].Score, 1e-9)
		assert.Equal(t, similarVal, resp.Results[0].Value)
	})
}
