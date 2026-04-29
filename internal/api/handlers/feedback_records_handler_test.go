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

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// mockFeedbackRecordsService mocks FeedbackRecordsService for handler tests.
type mockFeedbackRecordsService struct {
	createFunc     func(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	bulkDeleteFunc func(ctx context.Context, filters *models.BulkDeleteFilters) (int, error)
}

func (m *mockFeedbackRecordsService) CreateFeedbackRecord(
	ctx context.Context, req *models.CreateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}

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

func (m *mockFeedbackRecordsService) BulkDeleteFeedbackRecords(
	ctx context.Context, filters *models.BulkDeleteFilters,
) (int, error) {
	if m.bulkDeleteFunc != nil {
		return m.bulkDeleteFunc(ctx, filters)
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

func TestFeedbackRecordsHandler_Create(t *testing.T) {
	t.Run("success returns created record", func(t *testing.T) {
		recordID := uuid.Must(uuid.NewV7())
		mock := &mockFeedbackRecordsService{
			createFunc: func(_ context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
				assert.Equal(t, "org-123", req.TenantID)

				return &models.FeedbackRecord{
					ID:           recordID,
					SourceType:   req.SourceType,
					FieldID:      req.FieldID,
					FieldType:    req.FieldType,
					TenantID:     req.TenantID,
					SubmissionID: req.SubmissionID,
				}, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", feedbackRecordCreateBody(t, "org-123"),
		)
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusCreated, rec.Code)

		var got models.FeedbackRecord
		err := json.Unmarshal(rec.Body.Bytes(), &got)
		require.NoError(t, err)
		assert.Equal(t, recordID, got.ID)
		assert.Equal(t, "org-123", got.TenantID)
	})

	t.Run("service validation error returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			createFunc: func(_ context.Context, _ *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
				return nil, huberrors.NewValidationError("tenant_id", "tenant_id is required and cannot be empty")
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", feedbackRecordCreateBody(t, "   "),
		)
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	})

	t.Run("service conflict returns conflict", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			createFunc: func(_ context.Context, _ *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
				return nil, huberrors.NewConflictError("duplicate feedback record")
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", feedbackRecordCreateBody(t, "org-123"),
		)
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusConflict, rec.Code)
	})
}

func feedbackRecordCreateBody(t *testing.T, tenantID string) *bytes.Reader {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"source_type":   "formbricks",
		"submission_id": "submission-1",
		"tenant_id":     tenantID,
		"field_id":      "feedback",
		"field_type":    "text",
	})
	require.NoError(t, err)

	return bytes.NewReader(body)
}

func TestFeedbackRecordsHandler_BulkDelete(t *testing.T) {
	t.Run("success returns 200 with deleted_count and message", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, filters *models.BulkDeleteFilters) (int, error) {
				assert.Equal(t, "user-123", filters.UserID)
				assert.Nil(t, filters.TenantID)

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

	t.Run("optional tenant_id query parameter is passed to service", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, filters *models.BulkDeleteFilters) (int, error) {
				assert.Equal(t, "user-456", filters.UserID)
				require.NotNil(t, filters.TenantID)
				assert.Equal(t, "tenant-a", *filters.TenantID)

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

	t.Run("empty tenant_id returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-123&tenant_id=", http.NoBody)
		rec := httptest.NewRecorder()

		handler.BulkDelete(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
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
			bulkDeleteFunc: func(_ context.Context, _ *models.BulkDeleteFilters) (int, error) {
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
			bulkDeleteFunc: func(_ context.Context, _ *models.BulkDeleteFilters) (int, error) {
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
