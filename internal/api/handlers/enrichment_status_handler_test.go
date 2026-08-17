package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
			AsOf:        time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
			Translation: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			Sentiment:   models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 8},
			Emotions: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonNotConfigured,
			},
		}
		h := NewEnrichmentStatusHandler(&fakeEnrichmentStatusService{resp: want})
		rec := httptest.NewRecorder()
		h.GetStatus(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/enrichment-status?tenant_id=t1", nil))

		require.Equal(t, http.StatusOK, rec.Code)

		var got models.EnrichmentStatusResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, *want, got)
	})

	// Asserts the JSON the client actually receives, by field name, rather than round-tripping
	// through models.EnrichmentStatusResponse. That round trip is symmetric with the marshal that
	// produced it, so it cannot observe the wire format at all: it passes unchanged if a field is
	// renamed, dropped, or emitted with the wrong presence semantics. The published OpenAPI schema
	// is a promise about these bytes, and Stainless generates the SDK from it, so something has to
	// check them.
	t.Run("wire format matches the published schema", func(t *testing.T) {
		resp := &models.EnrichmentStatusResponse{
			TenantID:    "t1",
			AsOf:        time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
			Translation: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			Sentiment: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonSwitchedOff,
			},
			Emotions: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonNotConfigured,
			},
		}
		h := NewEnrichmentStatusHandler(&fakeEnrichmentStatusService{resp: resp})
		rec := httptest.NewRecorder()
		h.GetStatus(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/enrichment-status?tenant_id=t1", nil))

		require.Equal(t, http.StatusOK, rec.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

		assert.Equal(t, "t1", body["tenant_id"])
		// RFC 3339 in UTC, matching `format: date-time` in the schema. Parsing it back is what
		// proves the client can, which is the whole reason the field exists.
		asOf, parseErr := time.Parse(time.RFC3339Nano, body["as_of"].(string))
		require.NoError(t, parseErr, "as_of must be RFC 3339")
		assert.True(t, asOf.Equal(resp.AsOf), "as_of survives serialization unchanged")

		// An ENABLED enrichment must omit disabled_reason entirely. Emitting it as "" would be the
		// natural result of dropping `omitempty`, and "" is not one of the three values the schema's
		// enum allows — a strict client would reject the whole response.
		translation, ok := body["translation"].(map[string]any)
		require.True(t, ok, "translation is an object")

		_, present := translation["disabled_reason"]
		assert.False(t, present, "enabled enrichment must omit disabled_reason, not send an empty one")

		// A DISABLED enrichment must carry its reason, and it must be one of the enum values.
		allowed := map[string]bool{"not_configured": true, "switched_off": true, "no_target_language": true}
		wantReasons := map[string]string{"sentiment": "switched_off", "emotions": "not_configured"}

		for name, wantReason := range wantReasons {
			enrichment, isObj := body[name].(map[string]any)
			require.True(t, isObj, "%s is an object", name)

			reason, hasReason := enrichment["disabled_reason"].(string)
			require.True(t, hasReason, "%s: disabled enrichment must carry disabled_reason", name)
			assert.True(t, allowed[reason], "%s: %q is outside the schema enum", name, reason)
			assert.Equal(t, wantReason, reason, "%s: wrong reason on the wire", name)
		}
	})
}
