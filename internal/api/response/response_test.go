package response

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apivalidation "github.com/formbricks/hub/internal/api/validation"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/pkg/cursor"
)

func newReq(t *testing.T, method, target string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) ProblemDetails {
	t.Helper()

	var problem ProblemDetails

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))

	return problem
}

func TestRespondErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantType   string
	}{
		{
			name: "nil maps to internal", err: nil,
			wantStatus: http.StatusInternalServerError, wantCode: CodeInternalServerError, wantType: ProblemTypeInternalServerError,
		},
		{
			name: "not found", err: huberrors.NewNotFoundError("feedback record", "feedback record not found"),
			wantStatus: http.StatusNotFound, wantCode: CodeNotFound, wantType: ProblemTypeNotFound,
		},
		{
			name: "hub validation", err: huberrors.NewValidationError("tenant_id", "tenant_id is required"),
			wantStatus: http.StatusBadRequest, wantCode: CodeValidation, wantType: ProblemTypeValidation,
		},
		{
			name: "conflict", err: huberrors.NewConflictError("already exists"),
			wantStatus: http.StatusConflict, wantCode: CodeConflict, wantType: ProblemTypeConflict,
		},
		{
			name: "limit exceeded", err: huberrors.NewLimitExceededError("webhook limit reached"),
			wantStatus: http.StatusForbidden, wantCode: CodeForbidden, wantType: ProblemTypeForbidden,
		},
		{
			name: "invalid cursor", err: cursor.ErrInvalidCursor,
			wantStatus: http.StatusBadRequest, wantCode: CodeValidation, wantType: ProblemTypeValidation,
		},
		{
			name: "invalid field type", err: &models.InvalidFieldTypeError{Value: "textt"},
			wantStatus: http.StatusBadRequest, wantCode: CodeValidation, wantType: ProblemTypeValidation,
		},
		{
			name: "unknown error maps to internal", err: errors.New("boom"),
			wantStatus: http.StatusInternalServerError, wantCode: CodeInternalServerError, wantType: ProblemTypeInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			RespondError(rec, newReq(t, http.MethodGet, "/v1/resource"), tt.err)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

			problem := decodeProblem(t, rec)
			assert.Equal(t, tt.wantStatus, problem.Status)
			assert.Equal(t, tt.wantCode, problem.Code)
			assert.Equal(t, tt.wantType, problem.Type)
			assert.Equal(t, "/v1/resource", problem.Instance)
		})
	}
}

func TestRespondErrorNotFoundIncludesResourceDetails(t *testing.T) {
	rec := httptest.NewRecorder()

	RespondError(rec, newReq(t, http.MethodGet, "/v1/feedback-records/123"),
		huberrors.NewNotFoundError("feedback record", "feedback record not found"))

	problem := decodeProblem(t, rec)
	assert.Equal(t, "feedback record not found", problem.Detail)
	require.NotNil(t, problem.Details)
	assert.Equal(t, "feedback record", problem.Details["resource_type"])
}

func TestRespondErrorValidationInvalidParams(t *testing.T) {
	rec := httptest.NewRecorder()

	RespondError(rec, newReq(t, http.MethodPost, "/v1/feedback-records"),
		huberrors.NewValidationError("tenant_id", "tenant_id is required"))

	problem := decodeProblem(t, rec)
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "tenant_id", problem.InvalidParams[0].Name)
	assert.Equal(t, "tenant_id is required", problem.InvalidParams[0].Reason)
}

func TestRespondErrorInvalidFieldTypeReason(t *testing.T) {
	rec := httptest.NewRecorder()

	RespondError(rec, newReq(t, http.MethodPost, "/v1/feedback-records"), &models.InvalidFieldTypeError{Value: "textt"})

	problem := decodeProblem(t, rec)
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "field_type", problem.InvalidParams[0].Name)
	assert.Contains(t, problem.InvalidParams[0].Reason, "textt")
	assert.Contains(t, problem.InvalidParams[0].Reason, "text")
	assert.Contains(t, problem.InvalidParams[0].Reason, "date")
}

func TestRespondErrorQueryDecodeErrorIsValidationProblem(t *testing.T) {
	var filters struct {
		Since *time.Time `form:"since"`
	}

	queryReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x?since=not-a-date", http.NoBody)
	err := apivalidation.ValidateAndDecodeQueryParams(queryReq, &filters)
	require.Error(t, err)

	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodGet, "/v1/x"), err)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	problem := decodeProblem(t, rec)
	assert.Equal(t, CodeValidation, problem.Code)
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "since", problem.InvalidParams[0].Name)
	assert.Equal(t, "must be in RFC3339 (ISO 8601) format", problem.InvalidParams[0].Reason)
}

func TestRespondErrorJSONDecodeFailures(t *testing.T) {
	t.Run("syntax error is bad request", func(t *testing.T) {
		var dst struct{}

		dec := json.NewDecoder(strings.NewReader("{not json"))
		err := dec.Decode(&dst)
		require.Error(t, err)

		rec := httptest.NewRecorder()
		RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), err)

		problem := decodeProblem(t, rec)
		assert.Equal(t, http.StatusBadRequest, problem.Status)
		assert.Equal(t, CodeBadRequest, problem.Code)
		assert.Contains(t, problem.Detail, "Invalid JSON")
		assert.Empty(t, problem.InvalidParams)
	})

	t.Run("type mismatch is validation with invalid_params", func(t *testing.T) {
		var dst struct {
			TenantID string `json:"tenant_id"`
		}

		dec := json.NewDecoder(strings.NewReader(`{"tenant_id": 123}`))
		err := dec.Decode(&dst)
		require.Error(t, err)

		rec := httptest.NewRecorder()
		RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), err)

		problem := decodeProblem(t, rec)
		assert.Equal(t, http.StatusBadRequest, problem.Status)
		assert.Equal(t, CodeValidation, problem.Code)
		require.Len(t, problem.InvalidParams, 1)
		assert.Equal(t, "tenant_id", problem.InvalidParams[0].Name)
		assert.Contains(t, problem.InvalidParams[0].Reason, "string")
	})

	t.Run("unknown field is bad request", func(t *testing.T) {
		var dst struct {
			Query string `json:"query"`
		}

		dec := json.NewDecoder(strings.NewReader(`{"query":"x","unexpected":"y"}`))
		dec.DisallowUnknownFields()

		err := dec.Decode(&dst)
		require.Error(t, err)

		rec := httptest.NewRecorder()
		RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), err)

		problem := decodeProblem(t, rec)
		assert.Equal(t, http.StatusBadRequest, problem.Status)
		assert.Equal(t, CodeValidation, problem.Code)
		require.Len(t, problem.InvalidParams, 1)
		assert.Equal(t, "unexpected", problem.InvalidParams[0].Name)
		assert.Contains(t, problem.InvalidParams[0].Reason, "not a recognized")
	})
}

func TestRespondErrorPopulatesRequestIDFromContext(t *testing.T) {
	rec := httptest.NewRecorder()
	r := newReq(t, http.MethodGet, "/v1/resource")
	r = r.WithContext(context.WithValue(r.Context(), observability.RequestIDKey, "req-test-123"))

	RespondError(rec, r, huberrors.NewNotFoundError("feedback record", "not found"))

	problem := decodeProblem(t, rec)
	assert.Equal(t, "req-test-123", problem.RequestID)
	assert.Equal(t, "/v1/resource", problem.Instance)
}

func TestRespondErrorBodyOmitsLegacyAndSensitiveFields(t *testing.T) {
	rec := httptest.NewRecorder()
	r := newReq(t, http.MethodPost, "/v1/feedback-records")
	r = r.WithContext(context.WithValue(r.Context(), observability.RequestIDKey, "req-shape-1"))

	RespondError(rec, r, huberrors.NewValidationError("tenant_id", "tenant_id is required"))

	raw := rec.Body.String()
	assert.Contains(t, raw, `"invalid_params"`)
	assert.Contains(t, raw, `"name"`)
	assert.Contains(t, raw, `"reason"`)
	assert.Contains(t, raw, `"request_id"`)
	assert.Contains(t, raw, `"code"`)
	// Legacy RFC 7807 shape must be gone, and we never echo the offending value.
	assert.NotContains(t, raw, `"errors"`)
	assert.NotContains(t, raw, `"value"`)
	assert.NotContains(t, raw, `"location"`)
}

func TestRespondErrorDoesNotLeakInternalCause(t *testing.T) {
	rec := httptest.NewRecorder()

	RespondError(rec, newReq(t, http.MethodGet, "/v1/resource"), errors.New("connection refused to secret-db:5432"))

	problem := decodeProblem(t, rec)
	assert.Equal(t, "An unexpected error occurred", problem.Detail)
	assert.NotContains(t, rec.Body.String(), "secret-db")
}

func TestRespondErrorValidatorErrorsMapToInvalidParams(t *testing.T) {
	type body struct {
		Name string `json:"name" validate:"required"`
		Kind string `json:"kind" validate:"oneof=alpha"`
	}

	validate := validator.New()
	validate.RegisterTagNameFunc(jsonTagName)

	err := validate.Struct(body{Kind: "z"})
	require.Error(t, err)

	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), err)

	problem := decodeProblem(t, rec)
	assert.Equal(t, CodeValidation, problem.Code)
	require.Len(t, problem.InvalidParams, 2)

	reasons := map[string]string{}
	for _, p := range problem.InvalidParams {
		reasons[p.Name] = p.Reason
	}

	assert.Equal(t, "is required", reasons["name"])
	assert.Equal(t, "must be one of: alpha", reasons["kind"])
}

func TestRespondErrorNestedValidationFieldPath(t *testing.T) {
	type inner struct {
		Kind string `json:"kind" validate:"required"`
	}

	type outer struct {
		Items []inner `json:"items" validate:"dive"`
	}

	validate := validator.New()
	validate.RegisterTagNameFunc(jsonTagName)

	err := validate.Struct(outer{Items: []inner{{Kind: ""}}})
	require.Error(t, err)

	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), err)

	problem := decodeProblem(t, rec)
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "items[0].kind", problem.InvalidParams[0].Name)
	assert.Equal(t, "is required", problem.InvalidParams[0].Reason)
}

func TestFormatFieldErrorReasons(t *testing.T) {
	type body struct {
		Req   string `validate:"required"`
		Min   string `validate:"min=5"`
		Max   string `validate:"max=2"`
		Gte   int    `validate:"gte=10"`
		Lte   int    `validate:"lte=1"`
		One   string `validate:"oneof=alpha"`
		UUID  string `validate:"uuid"`
		HTTP  string `validate:"http_url"`
		URL   string `validate:"url"`
		Alnum string `validate:"alphanum"`
	}

	validate := validator.New()
	err := validate.Struct(body{Min: "x", Max: "toolong", Gte: 1, Lte: 5, One: "z", UUID: "nope", HTTP: "nope", URL: "nope", Alnum: "!!"})

	var validationErrors validator.ValidationErrors
	require.ErrorAs(t, err, &validationErrors)

	got := map[string]string{}
	for _, fieldErr := range validationErrors {
		got[fieldErr.Field()] = apivalidation.FormatFieldError(fieldErr)
	}

	assert.Equal(t, "is required", got["Req"])
	assert.Equal(t, "must be at least 5", got["Min"])
	assert.Equal(t, "must be at most 2", got["Max"])
	assert.Equal(t, "must be greater than or equal to 10", got["Gte"])
	assert.Equal(t, "must be less than or equal to 1", got["Lte"])
	assert.Equal(t, "must be one of: alpha", got["One"])
	assert.Equal(t, "must be a valid UUID", got["UUID"])
	assert.Equal(t, "must be a valid HTTP or HTTPS URL", got["HTTP"])
	assert.Equal(t, "must be a valid URL", got["URL"])
	assert.Equal(t, "is invalid", got["Alnum"])
}

func TestRespondProblemHelpers(t *testing.T) {
	tests := []struct {
		name       string
		respond    func(http.ResponseWriter, *http.Request)
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unauthorized",
			respond:    func(w http.ResponseWriter, r *http.Request) { RespondUnauthorized(w, r, "no") },
			wantStatus: http.StatusUnauthorized, wantCode: CodeUnauthorized,
		},
		{
			name:       "not found",
			respond:    func(w http.ResponseWriter, r *http.Request) { RespondNotFound(w, r, "missing") },
			wantStatus: http.StatusNotFound, wantCode: CodeNotFound,
		},
		{
			name:       "service unavailable",
			respond:    func(w http.ResponseWriter, r *http.Request) { RespondServiceUnavailable(w, r, "down") },
			wantStatus: http.StatusServiceUnavailable, wantCode: CodeServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.respond(rec, newReq(t, http.MethodGet, "/v1/x"))

			assert.Equal(t, tt.wantStatus, rec.Code)
			problem := decodeProblem(t, rec)
			assert.Equal(t, tt.wantCode, problem.Code)
			assert.Equal(t, tt.wantStatus, problem.Status)
		})
	}
}

func TestRespondInvalidParams(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondInvalidParams(rec, newReq(t, http.MethodGet, "/v1/x"),
		InvalidParam{Name: "id", Reason: "must be a valid UUID"},
		InvalidParam{Name: "tenant_id", Reason: "is required"},
	)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	problem := decodeProblem(t, rec)
	assert.Equal(t, CodeValidation, problem.Code)
	assert.Equal(t, ProblemTypeValidation, problem.Type)
	require.Len(t, problem.InvalidParams, 2)
	assert.Equal(t, "id", problem.InvalidParams[0].Name)
	assert.Equal(t, "tenant_id", problem.InvalidParams[1].Name)
}

func TestRespondErrorInvalidCursorIsValidationParam(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodGet, "/v1/x"), cursor.ErrInvalidCursor)

	problem := decodeProblem(t, rec)
	assert.Equal(t, CodeValidation, problem.Code)
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "cursor", problem.InvalidParams[0].Name)
}

func TestRespondErrorPayloadTooLarge(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), &http.MaxBytesError{Limit: 1048576})

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	problem := decodeProblem(t, rec)
	assert.Equal(t, CodePayloadTooLarge, problem.Code)
	assert.Contains(t, problem.Detail, "1048576")
}

func TestRespondErrorTruncatedJSONIsBadRequest(t *testing.T) {
	// A truncated body (`{"x":`) returns io.ErrUnexpectedEOF from the decoder,
	// which is a client mistake, not a server failure.
	var dst struct {
		X string `json:"x"`
	}

	dec := json.NewDecoder(strings.NewReader(`{"x":`))
	err := dec.Decode(&dst)
	require.Error(t, err)

	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), err)

	problem := decodeProblem(t, rec)
	assert.Equal(t, http.StatusBadRequest, problem.Status)
	assert.Equal(t, CodeBadRequest, problem.Code)
	assert.Contains(t, problem.Detail, "Invalid JSON")
}

func TestProblemResponseMirrorsRequestIDIntoHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	r := newReq(t, http.MethodGet, "/v1/x")
	r = r.WithContext(context.WithValue(r.Context(), observability.RequestIDKey, "req-mirror-1"))

	RespondError(rec, r, huberrors.NewNotFoundError("x", "not found"))

	problem := decodeProblem(t, rec)
	assert.Equal(t, "req-mirror-1", problem.RequestID)
	assert.Equal(t, "req-mirror-1", rec.Header().Get("X-Request-ID"))
}

func TestProblemResponseSetsNoStoreCacheControl(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodGet, "/v1/x"), huberrors.NewNotFoundError("x", "not found"))

	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestRespondUnauthorizedSetsWWWAuthenticateChallenge(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondUnauthorized(rec, newReq(t, http.MethodGet, "/v1/x"), "missing token")

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
}

func TestNonUnauthorizedProblemHasNoWWWAuthenticate(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodGet, "/v1/x"), huberrors.NewNotFoundError("x", "not found"))

	assert.Empty(t, rec.Header().Get("WWW-Authenticate"))
}

func TestCodeAndTypeForStatusDefaults(t *testing.T) {
	assert.Equal(t, CodePayloadTooLarge, codeForStatus(http.StatusRequestEntityTooLarge))
	assert.Equal(t, CodeMethodNotAllowed, codeForStatus(http.StatusMethodNotAllowed))
	// Unlisted client error falls back to bad_request / client-error type.
	assert.Equal(t, CodeBadRequest, codeForStatus(http.StatusTeapot))
	assert.Equal(t, ProblemTypeClientError, problemTypeForStatus(http.StatusTeapot))
	// Unlisted server error falls back to internal.
	assert.Equal(t, CodeInternalServerError, codeForStatus(http.StatusBadGateway))
	assert.Equal(t, ProblemTypeInternalServerError, problemTypeForStatus(http.StatusBadGateway))
}

func TestRespondJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondJSON(rec, http.StatusCreated, map[string]string{"id": "abc"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id":"abc"}`, rec.Body.String())
}

func TestRespondErrorLogsOnce(t *testing.T) {
	handler := &capturingHandler{}
	prev := slog.Default()

	slog.SetDefault(slog.New(handler))

	t.Cleanup(func() { slog.SetDefault(prev) })

	t.Run("server error logs cause at error level", func(t *testing.T) {
		handler.reset()

		rec := httptest.NewRecorder()
		RespondError(rec, newReq(t, http.MethodGet, "/v1/x"), errors.New("db down"))

		records := handler.snapshot()
		require.Len(t, records, 1)
		assert.Equal(t, slog.LevelError, records[0].Level)
		assert.Equal(t, "db down", attrValue(records[0], "error"))
		assert.Equal(t, CodeInternalServerError, attrValue(records[0], "code"))
	})

	t.Run("client error logs at warn level", func(t *testing.T) {
		handler.reset()

		rec := httptest.NewRecorder()
		RespondError(rec, newReq(t, http.MethodGet, "/v1/x"), cursor.ErrInvalidCursor)

		records := handler.snapshot()
		require.Len(t, records, 1)
		assert.Equal(t, slog.LevelWarn, records[0].Level)
		assert.Equal(t, CodeValidation, attrValue(records[0], "code"))
	})

	t.Run("server error without a cause logs at warn level", func(t *testing.T) {
		handler.reset()

		rec := httptest.NewRecorder()
		// A deliberate 503 (e.g. a disabled feature) carries no underlying cause and
		// should not be logged at Error, to avoid false alarms.
		RespondServiceUnavailable(rec, newReq(t, http.MethodGet, "/v1/x"), "feature disabled")

		records := handler.snapshot()
		require.Len(t, records, 1)
		assert.Equal(t, slog.LevelWarn, records[0].Level)
		assert.Equal(t, CodeServiceUnavailable, attrValue(records[0], "code"))
	})
}

// capturingHandler records slog records for assertions.
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r.Clone())

	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = nil
}

func (h *capturingHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]slog.Record(nil), h.records...)
}

func attrValue(r slog.Record, key string) string {
	var found string

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = a.Value.String()

			return false
		}

		return true
	})

	return found
}

func jsonTagName(field reflect.StructField) string {
	name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
	if name == "-" {
		return ""
	}

	return name
}
