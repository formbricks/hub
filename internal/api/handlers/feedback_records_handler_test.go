package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

// mockFeedbackRecordsService mocks FeedbackRecordsService for handler tests.
type mockFeedbackRecordsService struct {
	bulkDeleteFunc func(ctx context.Context, userID string) (int, error)
}

func (m *mockFeedbackRecordsService) CreateFeedbackRecord(
	context.Context, *models.CreateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) GetFeedbackRecord(context.Context, uuid.UUID) (*models.FeedbackRecord, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) ListFeedbackRecords(
	context.Context, *models.ListFeedbackRecordsFilters,
) (*models.ListFeedbackRecordsResponse, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) UpdateFeedbackRecord(
	context.Context, uuid.UUID, *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) DeleteFeedbackRecord(context.Context, uuid.UUID) error {
	return nil
}

func (m *mockFeedbackRecordsService) BulkDeleteFeedbackRecords(ctx context.Context, userID string) (int, error) {
	if m.bulkDeleteFunc != nil {
		return m.bulkDeleteFunc(ctx, userID)
	}

	return 0, nil
}

func TestFeedbackRecordsHandler_List(t *testing.T) {
	t.Run("missing tenant_id returns 400", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://test/v1/feedback-records", http.NoBody)
		rec := httptest.NewRecorder()

		handler.List(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestFeedbackRecordsHandler_BulkDelete(t *testing.T) {
	t.Run("success returns 200 with deleted_count and message", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, userID string) (int, error) {
				assert.Equal(t, "user-123", userID)

				return 3, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-123", http.NoBody)
		rec := httptest.NewRecorder()

		handler.BulkDelete(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp models.BulkDeleteResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, int64(3), resp.DeletedCount)
		assert.Equal(t, "Successfully deleted 3 feedback records", resp.Message)
	})

	t.Run("extra tenant_id query parameter is ignored", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, userID string) (int, error) {
				assert.Equal(t, "user-456", userID)

				return 1, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-456&tenant_id=tenant-a", http.NoBody)
		rec := httptest.NewRecorder()

		handler.BulkDelete(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("missing user_id returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "http://test/v1/feedback-records", http.NoBody)
		rec := httptest.NewRecorder()

		handler.BulkDelete(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("empty user_id returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=", http.NoBody)
		rec := httptest.NewRecorder()

		handler.BulkDelete(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, _ string) (int, error) {
				return 0, assert.AnError
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-789", http.NoBody)
		rec := httptest.NewRecorder()

		handler.BulkDelete(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("zero deleted returns 200 with deleted_count 0", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, _ string) (int, error) {
				return 0, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=nonexistent", http.NoBody)
		rec := httptest.NewRecorder()

		handler.BulkDelete(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp models.BulkDeleteResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, int64(0), resp.DeletedCount)
		assert.Equal(t, "Successfully deleted 0 feedback records", resp.Message)
	})
}
