package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockFeedbackRecordsPurgeService struct {
	enqueueFunc func(ctx context.Context, tenantID string) (*models.FeedbackRecordsPurgeAcceptedResponse, error)
	calls       int
}

func (m *mockFeedbackRecordsPurgeService) Enqueue(
	ctx context.Context, tenantID string,
) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
	m.calls++

	if m.enqueueFunc != nil {
		return m.enqueueFunc(ctx, tenantID)
	}

	return &models.FeedbackRecordsPurgeAcceptedResponse{
		TenantID: tenantID,
		Status:   models.FeedbackRecordsPurgeStatusAccepted,
	}, nil
}

func purgeRequest(body string) *http.Request {
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "http://test/v1/feedback-records/purge", strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	return req
}

func TestFeedbackRecordsPurgeHandler_Purge(t *testing.T) {
	t.Run("accepted returns 202 and does not report a deleted count", func(t *testing.T) {
		mock := &mockFeedbackRecordsPurgeService{
			enqueueFunc: func(_ context.Context, tenantID string) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
				assert.Equal(t, "org-123", tenantID)

				return &models.FeedbackRecordsPurgeAcceptedResponse{
					TenantID: "org-123",
					Status:   models.FeedbackRecordsPurgeStatusAccepted,
					Message:  "Feedback records purge accepted for org-123",
				}, nil
			},
		}

		rec := httptest.NewRecorder()
		NewFeedbackRecordsPurgeHandler(mock).Purge(rec, purgeRequest(`{"tenant_id":"org-123"}`))

		require.Equal(t, http.StatusAccepted, rec.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "org-123", body["tenant_id"])
		assert.Equal(t, models.FeedbackRecordsPurgeStatusAccepted, body["status"])
		// The purge has not run yet, so any count here would be a promise the endpoint cannot keep.
		assert.NotContains(t, body, "deleted_count")
	})

	// A missing or empty tenant_id must fail, never fall back to a wider scope.
	for _, testCase := range []struct {
		name string
		body string
	}{
		{name: "missing tenant_id", body: `{}`},
		{name: "empty tenant_id", body: `{"tenant_id":""}`},
		{name: "blank tenant_id", body: `{"tenant_id":"   "}`},
	} {
		t.Run(testCase.name+" is rejected without enqueueing", func(t *testing.T) {
			mock := &mockFeedbackRecordsPurgeService{
				enqueueFunc: func(_ context.Context, tenantID string) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
					// Reached only for values that pass body validation; the service still
					// normalizes, so a blank value is rejected here instead.
					if strings.TrimSpace(tenantID) == "" {
						return nil, huberrors.NewValidationError("tenant_id", "tenant_id is required")
					}

					return &models.FeedbackRecordsPurgeAcceptedResponse{TenantID: tenantID}, nil
				},
			}

			rec := httptest.NewRecorder()
			NewFeedbackRecordsPurgeHandler(mock).Purge(rec, purgeRequest(testCase.body))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}

	t.Run("unknown fields are rejected", func(t *testing.T) {
		mock := &mockFeedbackRecordsPurgeService{}

		rec := httptest.NewRecorder()
		NewFeedbackRecordsPurgeHandler(mock).Purge(rec, purgeRequest(`{"tenant_id":"org-123","user_id":"u-1"}`))

		// A caller narrowing the purge with a filter the endpoint does not honor must get an error,
		// not a silently wider deletion than they asked for.
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Zero(t, mock.calls)
	})

	t.Run("a tenant write conflict surfaces as a retryable 409", func(t *testing.T) {
		mock := &mockFeedbackRecordsPurgeService{
			enqueueFunc: func(_ context.Context, _ string) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
				return nil, huberrors.NewTenantWriteConflictError("tenant data purge in progress for this tenant; retry later")
			},
		}

		rec := httptest.NewRecorder()
		NewFeedbackRecordsPurgeHandler(mock).Purge(rec, purgeRequest(`{"tenant_id":"org-123"}`))

		require.Equal(t, http.StatusConflict, rec.Code)

		var problem map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
		assert.Equal(t, "tenant_write_conflict", problem["code"])
	})
}
