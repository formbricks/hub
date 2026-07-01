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

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockTenantDataService struct {
	deleteFunc func(ctx context.Context, tenantID string) (*models.TenantDataDeleteResult, error)
}

func (m *mockTenantDataService) DeleteTenantData(
	ctx context.Context, tenantID string,
) (*models.TenantDataDeleteResult, error) {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, tenantID)
	}

	return nil, nil
}

func TestTenantDataHandler_Delete(t *testing.T) {
	t.Run("success returns counts", func(t *testing.T) {
		mock := &mockTenantDataService{
			deleteFunc: func(_ context.Context, tenantID string) (*models.TenantDataDeleteResult, error) {
				assert.Equal(t, "org-123", tenantID)

				return &models.TenantDataDeleteResult{
					TenantID: "org-123",
					TenantDataDeleteCounts: models.TenantDataDeleteCounts{
						DeletedFeedbackRecords:            3,
						DeletedEmbeddings:                 2,
						DeletedWebhooks:                   1,
						DeletedTaxonomyRuns:               4,
						DeletedTaxonomyClusters:           5,
						DeletedTaxonomyClusterMemberships: 6,
						DeletedTaxonomyNodes:              7,
						DeletedTaxonomyActiveRuns:         8,
						DeletedTaxonomyNodeEvents:         9,
					},
				}, nil
			},
		}
		handler := NewTenantDataHandler(mock)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "http://test/v1/tenants/org-123/data", http.NoBody)
		req.SetPathValue("tenant_id", "org-123")

		rec := httptest.NewRecorder()

		handler.Delete(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp models.TenantDataDeleteResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, "org-123", resp.TenantID)
		assert.Equal(t, int64(3), resp.DeletedFeedbackRecords)
		assert.Equal(t, int64(2), resp.DeletedEmbeddings)
		assert.Equal(t, int64(1), resp.DeletedWebhooks)
		assert.Equal(t, int64(4), resp.DeletedTaxonomyRuns)
		assert.Equal(t, int64(5), resp.DeletedTaxonomyClusters)
		assert.Equal(t, int64(6), resp.DeletedTaxonomyClusterMemberships)
		assert.Equal(t, int64(7), resp.DeletedTaxonomyNodes)
		assert.Equal(t, int64(8), resp.DeletedTaxonomyActiveRuns)
		assert.Equal(t, int64(9), resp.DeletedTaxonomyNodeEvents)
		assert.Equal(t, "Successfully deleted tenant data for org-123", resp.Message)
	})

	t.Run("validation error returns bad request", func(t *testing.T) {
		mock := &mockTenantDataService{
			deleteFunc: func(context.Context, string) (*models.TenantDataDeleteResult, error) {
				return nil, huberrors.NewValidationError("tenant_id", "tenant_id is required and cannot be empty")
			},
		}
		handler := NewTenantDataHandler(mock)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "http://test/v1/tenants//data", http.NoBody)
		req.SetPathValue("tenant_id", "")

		rec := httptest.NewRecorder()

		handler.Delete(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")

		var problem response.ProblemDetails

		err := json.Unmarshal(rec.Body.Bytes(), &problem)
		require.NoError(t, err)
		assert.Equal(t, response.ProblemTypeValidation, problem.Type)
		assert.Equal(t, response.CodeValidation, problem.Code)
		require.Len(t, problem.InvalidParams, 1)
		assert.Equal(t, "tenant_id", problem.InvalidParams[0].Name)
		assert.Equal(t, "tenant_id is required and cannot be empty", problem.InvalidParams[0].Reason)
	})

	t.Run("unexpected service error returns internal server error", func(t *testing.T) {
		mock := &mockTenantDataService{
			deleteFunc: func(context.Context, string) (*models.TenantDataDeleteResult, error) {
				return nil, errors.New("database unavailable")
			},
		}
		handler := NewTenantDataHandler(mock)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "http://test/v1/tenants/org-123/data", http.NoBody)
		req.SetPathValue("tenant_id", "org-123")

		rec := httptest.NewRecorder()

		handler.Delete(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	})
}
