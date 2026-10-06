package response

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
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
			name: "tenant write conflict", err: huberrors.NewTenantWriteConflictError("tenant data purge in progress; retry later"),
			wantStatus: http.StatusConflict, wantCode: CodeTenantWriteConflict, wantType: ProblemTypeTenantWriteConflict,
		},
		{
			name:       "wrapped tenant write conflict keeps dedicated code",
			err:        fmt.Errorf("delete feedback record: %w", huberrors.NewTenantWriteConflictError("")),
			wantStatus: http.StatusConflict, wantCode: CodeTenantWriteConflict, wantType: ProblemTypeTenantWriteConflict,
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
			name: "cursor sort mismatch", err: cursor.ErrCursorSortMismatch,
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
		{
			name:       "internal error wrapping unexpected EOF stays internal",
			err:        fmt.Errorf("read repository payload: %w", io.ErrUnexpectedEOF),
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

// decodeV2 decodes the way handlers.decodeJSONBody does, so these cases see the errors the API
// actually produces.
func decodeV2(t *testing.T, body string, dst any) error {
	t.Helper()

	err := jsonv2.Unmarshal([]byte(body), dst, jsonv2.RejectUnknownMembers(true))
	require.Error(t, err)

	return NewRequestJSONDecodeError(err)
}

type decodeTarget struct {
	TenantID    string           `json:"tenant_id"`
	Count       *int             `json:"count,omitempty"`
	Diagnostics *decodeTargetSub `json:"diagnostics,omitempty"`
}

type decodeTargetSub struct {
	Model string `json:"model,omitempty"`
}

func TestRespondErrorJSONDecodeFailures(t *testing.T) {
	respond := func(t *testing.T, err error) ProblemDetails {
		t.Helper()

		rec := httptest.NewRecorder()
		RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), err)

		return decodeProblem(t, rec)
	}

	t.Run("syntax error is bad request", func(t *testing.T) {
		problem := respond(t, decodeV2(t, "{not json", &decodeTarget{}))

		assert.Equal(t, http.StatusBadRequest, problem.Status)
		assert.Equal(t, CodeBadRequest, problem.Code)
		assert.Contains(t, problem.Detail, "Invalid JSON")
		assert.Empty(t, problem.InvalidParams)
	})

	t.Run("truncated body is bad request", func(t *testing.T) {
		problem := respond(t, decodeV2(t, `{"tenant_id":`, &decodeTarget{}))

		assert.Equal(t, http.StatusBadRequest, problem.Status)
		assert.Equal(t, "Invalid JSON: unexpected end of input", problem.Detail)
	})

	t.Run("data after the top-level value is bad request", func(t *testing.T) {
		problem := respond(t, decodeV2(t, `{"tenant_id":"a"}{"tenant_id":"b"}`, &decodeTarget{}))

		assert.Equal(t, http.StatusBadRequest, problem.Status)
		assert.Contains(t, problem.Detail, "Invalid JSON")
		assert.Contains(t, problem.Detail, "after top-level value")
	})

	t.Run("type mismatch is validation with invalid_params", func(t *testing.T) {
		problem := respond(t, decodeV2(t, `{"tenant_id": 123}`, &decodeTarget{}))

		assert.Equal(t, http.StatusBadRequest, problem.Status)
		assert.Equal(t, CodeValidation, problem.Code)
		assert.Equal(t, []InvalidParam{{Name: "tenant_id", Reason: "must be string"}}, problem.InvalidParams)
	})

	t.Run("type mismatch names the pointed-to type and the nested path", func(t *testing.T) {
		count := respond(t, decodeV2(t, `{"count":"x"}`, &decodeTarget{}))
		nested := respond(t, decodeV2(t, `{"diagnostics":{"model":1}}`, &decodeTarget{}))

		assert.Equal(t, []InvalidParam{{Name: "count", Reason: "must be int"}}, count.InvalidParams)
		assert.Equal(t, []InvalidParam{{Name: "diagnostics.model", Reason: "must be string"}}, nested.InvalidParams)
	})

	t.Run("unknown member is validation naming it", func(t *testing.T) {
		problem := respond(t, decodeV2(t, `{"tenant_id":"a","TENANT_ID":"b"}`, &decodeTarget{}))
		nested := respond(t, decodeV2(t, `{"diagnostics":{"Model":"x"}}`, &decodeTarget{}))

		assert.Equal(t, CodeValidation, problem.Code)
		assert.Equal(t, []InvalidParam{{Name: "TENANT_ID", Reason: ReasonJSONMemberUnknown}}, problem.InvalidParams)
		assert.Equal(t, []InvalidParam{{Name: "diagnostics.Model", Reason: ReasonJSONMemberUnknown}}, nested.InvalidParams)
	})

	t.Run("repeated member is validation naming it, at any depth", func(t *testing.T) {
		top := respond(t, decodeV2(t, `{"tenant_id":"a","tenant_id":"b"}`, &decodeTarget{}))
		escaped := respond(t, decodeV2(t, `{"tenant_id":"a","tenant\u005fid":"b"}`, &decodeTarget{}))
		nested := respond(t, decodeV2(t, `{"diagnostics":{"model":"a","model":"b"}}`, &decodeTarget{}))

		assert.Equal(t, []InvalidParam{{Name: "tenant_id", Reason: ReasonJSONMemberRepeated}}, top.InvalidParams)
		assert.Equal(t, []InvalidParam{{Name: "tenant_id", Reason: ReasonJSONMemberRepeated}}, escaped.InvalidParams)
		assert.Equal(t, []InvalidParam{{Name: "diagnostics.model", Reason: ReasonJSONMemberRepeated}}, nested.InvalidParams)
	})

	t.Run("a body that is not an object is bad request", func(t *testing.T) {
		for _, body := range []string{`[]`, `"x"`, `1`, `true`} {
			problem := respond(t, decodeV2(t, body, &decodeTarget{}))

			assert.Equal(t, http.StatusBadRequest, problem.Status, body)
			assert.Equal(t, "Invalid request body: must be a JSON object", problem.Detail, body)
		}
	})

	t.Run("a custom decoder's refusal is reported at its position", func(t *testing.T) {
		err := NewRequestJSONDecodeError(&jsonv2.SemanticError{JSONPointer: "/event_types", Err: errors.New("invalid event type: x")})
		problem := respond(t, err)

		assert.Equal(t, []InvalidParam{{Name: "event_types", Reason: "invalid event type: x"}}, problem.InvalidParams)
	})

	t.Run("an over-long member name is truncated", func(t *testing.T) {
		long := strings.Repeat("X", 200)
		problem := respond(t, decodeV2(t, `{"`+long+`":1}`, &decodeTarget{}))

		require.Len(t, problem.InvalidParams, 1)
		assert.Equal(t, strings.Repeat("X", 64)+"…", problem.InvalidParams[0].Name)
	})

	t.Run("empty body is bad request", func(t *testing.T) {
		problem := respond(t, NewRequestJSONDecodeError(io.EOF))

		assert.Equal(t, http.StatusBadRequest, problem.Status)
		assert.Equal(t, CodeBadRequest, problem.Code)
		assert.Equal(t, "Invalid request body", problem.Detail)
	})

	t.Run("oversized body is 413", func(t *testing.T) {
		problem := respond(t, NewRequestJSONDecodeError(&http.MaxBytesError{Limit: 10}))

		assert.Equal(t, http.StatusRequestEntityTooLarge, problem.Status)
		assert.Equal(t, "request body too large", problem.Detail)
	})

	t.Run("raw json-like error is not treated as request decode", func(t *testing.T) {
		err := fmt.Errorf("downstream payload failed: %w", io.ErrUnexpectedEOF)

		problem := respond(t, err)
		assert.Equal(t, http.StatusInternalServerError, problem.Status)
		assert.Equal(t, CodeInternalServerError, problem.Code)
		assert.Equal(t, detailInternal, problem.Detail)
	})
}

// Member names are caller-controlled: a refusal must report them to the caller without writing
// them to the log, which carries the problem detail.
func TestRespondErrorJSONDecodeFailuresDoNotLogMemberNames(t *testing.T) {
	handler := &capturingHandler{}
	prev := slog.Default()

	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, body := range []string{
		`{"tenant_id":"a","SECRET_MEMBER_NAME":"b"}`,
		`{"SECRET_MEMBER_NAME":"a","SECRET_MEMBER_NAME":"b"}`,
		`{"diagnostics":{"SECRET_MEMBER_NAME":1}}`,
		`{"diagnostics":{"model":"a"},"SECRET_MEMBER_NAME":1} x`,
	} {
		rec := httptest.NewRecorder()
		RespondError(rec, newReq(t, http.MethodPost, "/v1/x"), decodeV2(t, body, &decodeTarget{}))
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
	}

	records := handler.snapshot()
	require.NotEmpty(t, records)

	for _, record := range records {
		record.Attrs(func(a slog.Attr) bool {
			assert.NotContains(t, a.Value.String(), "SECRET_MEMBER_NAME", "logged attribute %q", a.Key)

			return true
		})
		assert.NotContains(t, record.Message, "SECRET_MEMBER_NAME")
	}
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
	assert.Equal(t, InvalidCursorReason, problem.InvalidParams[0].Reason)
}

// A cursor presented under a different ordering than it was issued for is a distinct failure from
// a malformed one, and the two must not collapse: "start over" and "keep sort unchanged" are
// different instructions, and only one of them fixes the client's problem.
func TestRespondErrorCursorSortMismatchHasItsOwnReason(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondError(rec, newReq(t, http.MethodGet, "/v1/x"), cursor.ErrCursorSortMismatch)

	problem := decodeProblem(t, rec)
	assert.Equal(t, CodeValidation, problem.Code)
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "cursor", problem.InvalidParams[0].Name)
	assert.Equal(t, InvalidCursorSortReason, problem.InvalidParams[0].Reason)
	assert.NotEqual(t, InvalidCursorReason, problem.InvalidParams[0].Reason)
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
	assert.Equal(t, CodeMethodNotAllowed, codeForStatus(http.StatusMethodNotAllowed))
	assert.Equal(t, CodeContentTooLarge, codeForStatus(http.StatusRequestEntityTooLarge))
	assert.Equal(t, ProblemTypeContentTooLarge, problemTypeForStatus(http.StatusRequestEntityTooLarge))
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

	t.Run("caller log attrs are included once", func(t *testing.T) {
		handler.reset()

		rec := httptest.NewRecorder()
		RespondErrorWithLogAttrs(rec, newReq(t, http.MethodDelete, "/v1/feedback-records"), errors.New("db down"),
			"user_id_present", true,
			"user_id_length", 8,
			"tenant_id_present", false,
			"tenant_id_length", 0,
		)

		records := handler.snapshot()
		require.Len(t, records, 1)
		assert.Equal(t, slog.LevelError, records[0].Level)
		assert.Equal(t, "true", attrValue(records[0], "user_id_present"))
		assert.Equal(t, "8", attrValue(records[0], "user_id_length"))
		assert.Equal(t, "false", attrValue(records[0], "tenant_id_present"))
		assert.Equal(t, "0", attrValue(records[0], "tenant_id_length"))
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
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name == "-" {
		return ""
	}

	return name
}
