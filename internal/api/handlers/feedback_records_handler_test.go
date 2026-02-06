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
	bulkDeleteFunc func(ctx context.Context, userIdentifier string, tenantID *string) (int, error)
}

func (m *mockFeedbackRecordsService) CreateFeedbackRecord(context.Context, *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) GetFeedbackRecord(context.Context, uuid.UUID) (*models.FeedbackRecord, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) ListFeedbackRecords(context.Context, *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) UpdateFeedbackRecord(context.Context, uuid.UUID, *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) DeleteFeedbackRecord(context.Context, uuid.UUID) error {
	return nil
}

func (m *mockFeedbackRecordsService) BulkDeleteFeedbackRecords(ctx context.Context, userIdentifier string, tenantID *string) (int, error) {
	if m.bulkDeleteFunc != nil {
		return m.bulkDeleteFunc(ctx, userIdentifier, tenantID)
	}

	return 0, nil
}

func TestFeedbackRecordsHandler_BulkDelete(t *testing.T) {
	t.Run("success returns 200 with deleted_count and message", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, userIdentifier string, _ *string) (int, error) {
				assert.Equal(t, "user-123", userIdentifier)

				return 3, nil
			},
		}
		h := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequest(http.MethodDelete, "http://test/v1/feedback-records?user_identifier=user-123", http.NoBody)
		rec := httptest.NewRecorder()

		h.BulkDelete(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp models.BulkDeleteResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, int64(3), resp.DeletedCount)
		assert.Equal(t, "Successfully deleted 3 feedback records", resp.Message)
	})

	t.Run("success with tenant_id passes tenant to service", func(t *testing.T) {
		var capturedTenantID *string

		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, userIdentifier string, tenantID *string) (int, error) {
				assert.Equal(t, "user-456", userIdentifier)

				capturedTenantID = tenantID

				return 1, nil
			},
		}
		h := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequest(http.MethodDelete, "http://test/v1/feedback-records?user_identifier=user-456&tenant_id=tenant-a", http.NoBody)
		rec := httptest.NewRecorder()

		h.BulkDelete(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, capturedTenantID)
		assert.Equal(t, "tenant-a", *capturedTenantID)
	})

	t.Run("missing user_identifier returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		h := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequest(http.MethodDelete, "http://test/v1/feedback-records", http.NoBody)
		rec := httptest.NewRecorder()

		h.BulkDelete(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("empty user_identifier returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		h := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequest(http.MethodDelete, "http://test/v1/feedback-records?user_identifier=", http.NoBody)
		rec := httptest.NewRecorder()

		h.BulkDelete(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, _ string, _ *string) (int, error) {
				return 0, assert.AnError
			},
		}
		h := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequest(http.MethodDelete, "http://test/v1/feedback-records?user_identifier=user-789", http.NoBody)
		rec := httptest.NewRecorder()

		h.BulkDelete(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("zero deleted returns 200 with deleted_count 0", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			bulkDeleteFunc: func(_ context.Context, _ string, _ *string) (int, error) {
				return 0, nil
			},
		}
		h := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequest(http.MethodDelete, "http://test/v1/feedback-records?user_identifier=nonexistent", http.NoBody)
		rec := httptest.NewRecorder()

		h.BulkDelete(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp models.BulkDeleteResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, int64(0), resp.DeletedCount)
		assert.Equal(t, "Successfully deleted 0 feedback records", resp.Message)
	})
}
