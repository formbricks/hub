package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

// The gateway authorizes the exact `tenant_id` member and forwards the body unchanged, so a handler
// must refuse a body naming a second tenant under another spelling before any service call
// (ENG-3658).
const ambiguousTenantRecordBody = `{"source_type":"survey","field_id":"q1","field_type":"text",` +
	`"submission_id":"s1","tenant_id":"tenant-a","TENANT_ID":"tenant-b"}`

func assertRefusedMember(t *testing.T, rec *httptest.ResponseRecorder, member string) {
	t.Helper()

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var problem response.ProblemDetails
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	assert.Equal(t, []response.InvalidParam{{Name: member, Reason: response.ReasonJSONMemberUnknown}}, problem.InvalidParams)
}

func TestHandlersRefuseCaseVariantTenant(t *testing.T) {
	t.Run("feedback record create", func(t *testing.T) {
		called := false
		handler := NewFeedbackRecordsHandler(&mockFeedbackRecordsService{
			createFunc: func(context.Context, *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
				called = true

				return &models.FeedbackRecord{}, nil
			},
		})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/feedback-records",
			strings.NewReader(ambiguousTenantRecordBody))
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assertRefusedMember(t, rec, "TENANT_ID")
		assert.False(t, called, "the service must not see an ambiguous body")
	})

	t.Run("semantic search", func(t *testing.T) {
		called := false
		handler := NewSearchHandler(&mockSearchService{
			semanticFunc: func(context.Context, string, string, int, float64, string) (service.SearchResult, error) {
				called = true

				return service.SearchResult{}, nil
			},
		})

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"http://test/v1/feedback-records/search/semantic",
			strings.NewReader(`{"query":"q","tenant_id":"tenant-a","Tenant_Id":"tenant-b"}`))
		rec := httptest.NewRecorder()

		handler.SemanticSearch(rec, req)

		assertRefusedMember(t, rec, "Tenant_Id")
		assert.False(t, called, "the service must not see an ambiguous body")
	})

	t.Run("webhook create", func(t *testing.T) {
		webhooks := &recordingWebhooksService{}
		handler := NewWebhooksHandler(webhooks)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/webhooks",
			strings.NewReader(`{"url":"https://example.com/hook","tenant_id":"tenant-a","TENANT_ID":"tenant-b"}`))
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assertRefusedMember(t, rec, "TENANT_ID")
		assert.False(t, webhooks.called, "the service must not see an ambiguous body")
	})
}

// Webhook requests decode through their own UnmarshalJSON, which DisallowUnknownFields does not
// reach on its own; a misspelled member must still be refused there.
func TestWebhookRequestsRefuseUnknownMembers(t *testing.T) {
	body := `{"url":"https://example.com/hook","tenant_id":"tenant-a","tenantid":"tenant-b"}`

	for name, call := range map[string]func(*WebhooksHandler, http.ResponseWriter, *http.Request){
		"create": (*WebhooksHandler).Create,
		"update": (*WebhooksHandler).Update,
	} {
		t.Run(name, func(t *testing.T) {
			webhooks := &recordingWebhooksService{}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/webhooks",
				strings.NewReader(body))
			req.SetPathValue("id", uuid.Must(uuid.NewV7()).String())

			rec := httptest.NewRecorder()

			call(NewWebhooksHandler(webhooks), rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)

			var problem response.ProblemDetails
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
			assert.Equal(t, []response.InvalidParam{{Name: "tenantid", Reason: "is not a recognized request field"}}, problem.InvalidParams)
			assert.False(t, webhooks.called, "the service must not see a body with an unknown member")
		})
	}
}

type recordingRetryService struct{ called bool }

func (s *recordingRetryService) Retry(context.Context, string, []string) (*models.EnrichmentRetryResponse, error) {
	s.called = true

	return &models.EnrichmentRetryResponse{}, nil
}

// A JSON null body made the webhook request decoders dereference a nil pointer before ENG-3658
// (`json.Unmarshal(data, &aux)` sets aux to nil). It must be an ordinary validation failure.
func TestWebhookRequestsWithNullBodyAreRefusedCleanly(t *testing.T) {
	for name, call := range map[string]func(*WebhooksHandler, http.ResponseWriter, *http.Request){
		"create": (*WebhooksHandler).Create,
		"update": (*WebhooksHandler).Update,
	} {
		t.Run(name, func(t *testing.T) {
			webhooks := &recordingWebhooksService{}
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/webhooks",
				strings.NewReader(`null`))
			req.SetPathValue("id", uuid.Must(uuid.NewV7()).String())

			rec := httptest.NewRecorder()

			require.NotPanics(t, func() { call(NewWebhooksHandler(webhooks), rec, req) })

			// null decodes as the zero value, so it must behave exactly like an empty object.
			emptyReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/webhooks",
				strings.NewReader(`{}`))
			emptyReq.SetPathValue("id", req.PathValue("id"))

			emptyRec := httptest.NewRecorder()
			call(NewWebhooksHandler(&recordingWebhooksService{}), emptyRec, emptyReq)

			assert.Equal(t, emptyRec.Code, rec.Code)
			assert.Less(t, rec.Code, http.StatusInternalServerError)

			if name == "create" {
				assert.Equal(t, http.StatusBadRequest, rec.Code, "url and tenant_id are required")
				assert.False(t, webhooks.called)
			}
		})
	}
}

func TestWebhookRequestsNameAnInvalidEventType(t *testing.T) {
	webhooks := &recordingWebhooksService{}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/webhooks",
		strings.NewReader(`{"url":"https://example.com/hook","tenant_id":"tenant-a","event_types":["not.an.event"]}`))
	rec := httptest.NewRecorder()

	NewWebhooksHandler(webhooks).Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var problem response.ProblemDetails
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "event_types", problem.InvalidParams[0].Name)
	assert.Contains(t, problem.InvalidParams[0].Reason, "not.an.event")
	assert.False(t, webhooks.called)
}

func TestEnrichmentRetryBody(t *testing.T) {
	retry := func(t *testing.T, body string) (*httptest.ResponseRecorder, *recordingRetryService) {
		t.Helper()

		service := &recordingRetryService{}
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
			"http://test/v1/tenants/t/enrichments/retry", strings.NewReader(body))
		req.SetPathValue("tenant_id", "t")

		rec := httptest.NewRecorder()
		NewEnrichmentRetryHandler(service).Retry(rec, req)

		return rec, service
	}

	t.Run("an empty body still means every enrichment", func(t *testing.T) {
		rec, service := retry(t, "")

		assert.Equal(t, http.StatusAccepted, rec.Code)
		assert.True(t, service.called)
	})

	t.Run("names a misspelled member instead of calling the body not an object", func(t *testing.T) {
		rec, service := retry(t, `{"Enrichments":["sentiment"]}`)

		assertRefusedMember(t, rec, "Enrichments")
		assert.False(t, service.called)
	})

	t.Run("a JSON null still means every enrichment, as before", func(t *testing.T) {
		rec, service := retry(t, `null`)

		assert.Equal(t, http.StatusAccepted, rec.Code)
		assert.True(t, service.called)
	})
}

func TestSemanticSearchBodyIsBounded(t *testing.T) {
	handler := NewSearchHandler(&mockSearchService{})

	body := `{"tenant_id":"tenant-a","query":"` + strings.Repeat("a", maxSmallJSONBodyBytes) + `"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://test/v1/feedback-records/search/semantic", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.SemanticSearch(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

type recordingWebhooksService struct{ called bool }

func (s *recordingWebhooksService) CreateWebhook(context.Context, *models.CreateWebhookRequest) (*models.Webhook, error) {
	s.called = true

	return &models.Webhook{}, nil
}

func (s *recordingWebhooksService) GetWebhook(context.Context, uuid.UUID) (*models.Webhook, error) {
	s.called = true

	return &models.Webhook{}, nil
}

func (s *recordingWebhooksService) ListWebhooks(context.Context, *models.ListWebhooksFilters) (*models.ListWebhooksResponse, error) {
	s.called = true

	return &models.ListWebhooksResponse{}, nil
}

func (s *recordingWebhooksService) UpdateWebhook(context.Context, uuid.UUID, *models.UpdateWebhookRequest) (*models.Webhook, error) {
	s.called = true

	return &models.Webhook{}, nil
}

func (s *recordingWebhooksService) DeleteWebhook(context.Context, uuid.UUID) error {
	s.called = true

	return nil
}
