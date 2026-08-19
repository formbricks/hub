package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockFeedbackRecordsPurgeService struct {
	enqueueFunc func(ctx context.Context, tenantID string) (*models.FeedbackRecordsPurgeAcceptedResponse, error)
	tenantIDs   []string
}

func (m *mockFeedbackRecordsPurgeService) Enqueue(
	ctx context.Context, tenantID string,
) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
	m.tenantIDs = append(m.tenantIDs, tenantID)

	if m.enqueueFunc != nil {
		return m.enqueueFunc(ctx, tenantID)
	}

	return &models.FeedbackRecordsPurgeAcceptedResponse{
		TenantID: tenantID,
		Status:   models.FeedbackRecordsPurgeStatusAccepted,
	}, nil
}

// The handler reads the tenant from the path value, not the URL, so the URL stays fixed — a raw
// blank tenant in the path would not even be a parseable request line.
func purgeRequest(tenantID string) *http.Request {
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodDelete,
		"http://test/v1/tenants/tenant/feedback-records", http.NoBody,
	)
	req.SetPathValue("tenant_id", tenantID)

	return req
}

func TestFeedbackRecordsPurgeHandler_Purge(t *testing.T) {
	t.Run("accepted returns 202 and does not report a deleted count", func(t *testing.T) {
		mock := &mockFeedbackRecordsPurgeService{
			enqueueFunc: func(_ context.Context, tenantID string) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
				return &models.FeedbackRecordsPurgeAcceptedResponse{
					TenantID: tenantID,
					Status:   models.FeedbackRecordsPurgeStatusAccepted,
					Message:  "Feedback records purge accepted for " + tenantID,
				}, nil
			},
		}

		rec := httptest.NewRecorder()
		NewFeedbackRecordsPurgeHandler(mock).Purge(rec, purgeRequest("org-123"))

		require.Equal(t, http.StatusAccepted, rec.Code)
		assert.Equal(t, []string{"org-123"}, mock.tenantIDs)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "org-123", body["tenant_id"])
		assert.Equal(t, models.FeedbackRecordsPurgeStatusAccepted, body["status"])
		// The purge has not run yet, so any count here would be a promise the endpoint cannot keep.
		assert.NotContains(t, body, "deleted_count")
	})

	// The tenant is a path segment, so it cannot be omitted — the request would route elsewhere.
	// An empty or blank value still has to be rejected rather than treated as "all tenants".
	for _, testCase := range []struct {
		name     string
		tenantID string
	}{
		{name: "empty tenant", tenantID: ""},
		{name: "blank tenant", tenantID: "   "},
	} {
		t.Run(testCase.name+" is rejected", func(t *testing.T) {
			mock := &mockFeedbackRecordsPurgeService{
				enqueueFunc: func(_ context.Context, _ string) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
					return nil, huberrors.NewValidationError("tenant_id", "tenant_id is required")
				},
			}

			rec := httptest.NewRecorder()
			NewFeedbackRecordsPurgeHandler(mock).Purge(rec, purgeRequest(testCase.tenantID))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}

	// Accepting a purge takes no tenant lock — the exclusive lock is taken by the worker — so this
	// endpoint has no 409, and the spec documents none. A failure to schedule must not read as
	// success, because the caller's next signal is the record count, which would look unchanged for
	// a purge that was never queued.
	t.Run("a scheduling failure is an error, not an accepted purge", func(t *testing.T) {
		mock := &mockFeedbackRecordsPurgeService{
			enqueueFunc: func(_ context.Context, _ string) (*models.FeedbackRecordsPurgeAcceptedResponse, error) {
				return nil, errors.New("queue unavailable")
			},
		}

		rec := httptest.NewRecorder()
		NewFeedbackRecordsPurgeHandler(mock).Purge(rec, purgeRequest("org-123"))

		require.Equal(t, http.StatusInternalServerError, rec.Code)

		var problem map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
		// The internal failure reason is never relayed to the caller.
		assert.NotContains(t, problem["detail"], "queue unavailable")
	})
}
