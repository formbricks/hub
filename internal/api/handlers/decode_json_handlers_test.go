package handlers

import (
	"context"
	"encoding/json"
	"fmt"
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

	// A webhook update can move the webhook to another tenant.
	t.Run("webhook update", func(t *testing.T) {
		webhooks := &recordingWebhooksService{}
		handler := NewWebhooksHandler(webhooks)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "http://test/v1/webhooks/x",
			strings.NewReader(`{"tenant_id":"tenant-a","TENANT_ID":"tenant-b"}`))
		req.SetPathValue("id", uuid.Must(uuid.NewV7()).String())

		rec := httptest.NewRecorder()

		handler.Update(rec, req)

		assertRefusedMember(t, rec, "TENANT_ID")
		assert.False(t, webhooks.called, "the service must not see an ambiguous body")
	})

	// tenant_id arrives here through the embedded TaxonomyScope, which v2 resolves by inlining.
	// The stub service panics if called, so reaching it fails the test.
	t.Run("taxonomy run create", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/taxonomy/runs",
			strings.NewReader(`{"tenant_id":"tenant-a","source_type":"survey","source_id":"s","field_id":"q1",`+
				`"actor_id":"a","TENANT_ID":"tenant-b"}`))
		rec := httptest.NewRecorder()

		NewTaxonomyHandler(&stubTaxonomyService{}).CreateRun(rec, req)

		assertRefusedMember(t, rec, "TENANT_ID")
	})

	t.Run("taxonomy node rename", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "http://test/v1/taxonomy/nodes/x",
			strings.NewReader(`{"tenant_id":"tenant-a","actor_id":"a","label":"x","Tenant_Id":"tenant-b"}`))
		req.SetPathValue("node_id", uuid.Must(uuid.NewV7()).String())

		rec := httptest.NewRecorder()

		NewTaxonomyHandler(&stubTaxonomyService{}).RenameNode(rec, req)

		assertRefusedMember(t, rec, "Tenant_Id")
	})
}

// Webhook requests decode through their own UnmarshalJSONFrom; the shared decoder's options must
// reach the members decoded there, so a misspelled member is refused like everywhere else.
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

// stubTaxonomyService satisfies TaxonomyService so the handlers reach their body decode; any call
// into it panics, which an oversized body must never get far enough to cause.
type stubTaxonomyService struct{ TaxonomyService }

// The bodies that had no cap before ENG-3658 must refuse anything over maxSmallJSONBodyBytes with 413
// before a service sees it — each call site passes its own limit, so each is pinned.
func TestNewlyCappedBodiesAreBounded(t *testing.T) {
	oversized := `{"x":"` + strings.Repeat("a", maxSmallJSONBodyBytes) + `"}`
	id := uuid.Must(uuid.NewV7()).String()

	cases := map[string]func(http.ResponseWriter, *http.Request){
		"semantic search":      NewSearchHandler(&mockSearchService{}).SemanticSearch,
		"webhook create":       NewWebhooksHandler(&recordingWebhooksService{}).Create,
		"webhook update":       NewWebhooksHandler(&recordingWebhooksService{}).Update,
		"taxonomy run create":  NewTaxonomyHandler(&stubTaxonomyService{}).CreateRun,
		"taxonomy node rename": NewTaxonomyHandler(&stubTaxonomyService{}).RenameNode,
		"taxonomy run failed":  NewTaxonomyInternalHandler(&taxonomyInternalHandlerTestService{}).FailRun,
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/x",
				strings.NewReader(oversized))
			req.SetPathValue("id", id)
			req.SetPathValue("node_id", id)
			req.SetPathValue("run_id", id)

			rec := httptest.NewRecorder()

			require.NotPanics(t, func() { call(rec, req) }, "the body must be refused before any service call")

			assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		})
	}
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

// The decoder turns invalid UTF-8 and unpaired surrogates in values into U+FFFD, where a JavaScript
// gateway keeps the surrogate or replaces a broken sequence with a single U+FFFD — so the two can read
// different tenants from the same bytes. Every body tenant_id refuses U+FFFD before a service call.
func TestHandlersRefuseMangledTenant(t *testing.T) {
	const reason = "must be valid UTF-8, with no unpaired UTF-16 surrogates or U+FFFD characters"

	id := uuid.Must(uuid.NewV7()).String()
	handlers := map[string]struct {
		call  func(http.ResponseWriter, *http.Request)
		body  string
		param string
	}{
		"feedback record create": {
			NewFeedbackRecordsHandler(&mockFeedbackRecordsService{}).Create,
			`{"source_type":"survey","field_id":"q1","field_type":"text","submission_id":"s1","tenant_id":%s}`, "tenant_id",
		},
		"semantic search": {NewSearchHandler(&mockSearchService{}).SemanticSearch, `{"query":"q","tenant_id":%s}`, "tenant_id"},
		"webhook create": {
			NewWebhooksHandler(&recordingWebhooksService{}).Create, `{"url":"https://example.com/hook","tenant_id":%s}`, "tenant_id",
		},
		"webhook update": {NewWebhooksHandler(&recordingWebhooksService{}).Update, `{"tenant_id":%s}`, "tenant_id"},
		// The validator names a member of an embedded struct with the struct's name, as it does for
		// every validation failure on this route.
		"taxonomy run create": {
			NewTaxonomyHandler(&stubTaxonomyService{}).CreateRun,
			`{"tenant_id":%s,"source_type":"survey","source_id":"s","field_id":"q1","actor_id":"a"}`, "TaxonomyScope.tenant_id",
		},
		"taxonomy node rename": {
			NewTaxonomyHandler(&stubTaxonomyService{}).RenameNode, `{"tenant_id":%s,"actor_id":"a","label":"x"}`, "tenant_id",
		},
	}
	tenants := map[string]string{
		"invalid UTF-8":           `"tenant-a` + "\xe2\x82" + `"`,
		"an unpaired surrogate":   `"tenant-a` + backslash + `ud800"`,
		"a literal U+FFFD":        `"tenant-a` + string(rune(0xFFFD)) + `"`,
		"an escaped U+FFFD (too)": `"tenant-a` + backslash + `ufffd"`,
	}

	for name, handler := range handlers {
		for kind, tenant := range tenants {
			t.Run(name+", "+kind, func(t *testing.T) {
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/x",
					strings.NewReader(fmt.Sprintf(handler.body, tenant)))
				req.SetPathValue("id", id)
				req.SetPathValue("node_id", id)

				rec := httptest.NewRecorder()

				require.NotPanics(t, func() { handler.call(rec, req) }, "the body must be refused before any service call")

				assert.Equal(t, http.StatusBadRequest, rec.Code)

				var problem response.ProblemDetails
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
				assert.Equal(t, []response.InvalidParam{{Name: handler.param, Reason: reason}}, problem.InvalidParams)
			})
		}
	}
}
