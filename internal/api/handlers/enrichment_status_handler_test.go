package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type fakeEnrichmentStatusService struct {
	resp *models.EnrichmentStatusResponse
	err  error
}

func (f *fakeEnrichmentStatusService) GetEnrichmentStatus(
	_ context.Context, _ string,
) (*models.EnrichmentStatusResponse, error) {
	return f.resp, f.err
}

func TestEnrichmentStatusHandler_GetStatus(t *testing.T) {
	t.Run("service unavailable when not configured", func(t *testing.T) {
		h := NewEnrichmentStatusHandler(nil)
		rec := httptest.NewRecorder()
		h.GetStatus(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/enrichment-status?tenant_id=t1", nil))

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("missing tenant_id maps to 400", func(t *testing.T) {
		// The service returns a huberrors.ValidationError for a blank tenant_id; the handler must
		// surface it as a 400 via response.RespondError, not a 500.
		h := NewEnrichmentStatusHandler(&fakeEnrichmentStatusService{
			err: huberrors.NewValidationError("tenant_id", "tenant_id is required and cannot be empty"),
		})
		rec := httptest.NewRecorder()
		h.GetStatus(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/enrichment-status", nil))

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("returns 200 with the status body", func(t *testing.T) {
		want := &models.EnrichmentStatusResponse{
			TenantID:    "t1",
			Translation: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			Sentiment:   models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 8},
			Emotions:    models.EnrichmentTypeStatus{Enabled: false},
		}
		h := NewEnrichmentStatusHandler(&fakeEnrichmentStatusService{resp: want})
		rec := httptest.NewRecorder()
		h.GetStatus(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/enrichment-status?tenant_id=t1", nil))

		require.Equal(t, http.StatusOK, rec.Code)

		var got models.EnrichmentStatusResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, *want, got)
	})
}
