// Package response provides HTTP response helpers and RFC 9457 problem details.
package response

import "net/http"

// Problem type URIs. Each problem type has a stable URI that identifies and
// (eventually) documents the error class per RFC 9457 §3.1.1. The URI is the
// type member; clients and agents should branch on the machine-readable Code,
// not on Type or Title.
const (
	ProblemTypeValidation          = "https://hub.formbricks.com/problems/validation"
	ProblemTypeBadRequest          = "https://hub.formbricks.com/problems/bad-request"
	ProblemTypeUnauthorized        = "https://hub.formbricks.com/problems/unauthorized"
	ProblemTypeForbidden           = "https://hub.formbricks.com/problems/forbidden"
	ProblemTypeNotFound            = "https://hub.formbricks.com/problems/not-found"
	ProblemTypeConflict            = "https://hub.formbricks.com/problems/conflict"
	ProblemTypeMethodNotAllowed    = "https://hub.formbricks.com/problems/method-not-allowed"
	ProblemTypePayloadTooLarge     = "https://hub.formbricks.com/problems/payload-too-large"
	ProblemTypeServiceUnavailable  = "https://hub.formbricks.com/problems/service-unavailable"
	ProblemTypeInternalServerError = "https://hub.formbricks.com/problems/internal-server-error"
	ProblemTypeClientError         = "https://hub.formbricks.com/problems/client-error"
)

// Machine-readable, stable error codes carried in the RFC 9457 "code" extension
// member. This is the primary signal clients and agents should branch on; the
// set is closed and mirrored as an enum in the OpenAPI schema so the generated
// SDK exposes it as a union type.
const (
	CodeValidation          = "validation"
	CodeBadRequest          = "bad_request"
	CodeUnauthorized        = "unauthorized"
	CodeForbidden           = "forbidden"
	CodeNotFound            = "not_found"
	CodeConflict            = "conflict"
	CodeMethodNotAllowed    = "method_not_allowed"
	CodePayloadTooLarge     = "payload_too_large"
	CodeServiceUnavailable  = "service_unavailable"
	CodeInternalServerError = "internal_server_error"
)

const problemContentType = "application/problem+json"

// InvalidParam is an entry in the RFC 9457 "invalid_params" extension member.
// Name is the dotted path to the offending request field (e.g. "field_type");
// Reason is a self-correcting explanation of how to fix it, including allowed
// values or constraints where applicable so an agent can retry without guessing.
type InvalidParam struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ProblemDetails is an RFC 9457 (application/problem+json) response body with
// Formbricks extension members: code, request_id, details, and invalid_params.
type ProblemDetails struct {
	Type          string         `json:"type,omitempty"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Detail        string         `json:"detail,omitempty"`
	Instance      string         `json:"instance,omitempty"`
	Code          string         `json:"code"`
	RequestID     string         `json:"request_id"`
	Details       map[string]any `json:"details,omitempty"`
	InvalidParams []InvalidParam `json:"invalid_params,omitempty"`
}

// newProblem builds a ProblemDetails for an HTTP status, deriving the type URI,
// title, and machine-readable code. Callers set Detail/Details/InvalidParams.
func newProblem(status int, detail string) ProblemDetails {
	return ProblemDetails{
		Type:   problemTypeForStatus(status),
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Code:   codeForStatus(status),
	}
}

// newValidationProblem builds a 400 problem with the dedicated validation type
// and code. Callers attach InvalidParams describing the offending fields.
func newValidationProblem() ProblemDetails {
	return ProblemDetails{
		Type:   ProblemTypeValidation,
		Title:  "Validation Error",
		Status: http.StatusBadRequest,
		Detail: detailValidation,
		Code:   CodeValidation,
	}
}

// codeForStatus maps an HTTP status to its stable machine-readable code.
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusServiceUnavailable:
		return CodeServiceUnavailable
	case http.StatusInternalServerError:
		return CodeInternalServerError
	default:
		if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
			return CodeBadRequest
		}

		return CodeInternalServerError
	}
}

// problemTypeForStatus maps an HTTP status to its problem type URI.
func problemTypeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return ProblemTypeBadRequest
	case http.StatusUnauthorized:
		return ProblemTypeUnauthorized
	case http.StatusForbidden:
		return ProblemTypeForbidden
	case http.StatusNotFound:
		return ProblemTypeNotFound
	case http.StatusConflict:
		return ProblemTypeConflict
	case http.StatusMethodNotAllowed:
		return ProblemTypeMethodNotAllowed
	case http.StatusRequestEntityTooLarge:
		return ProblemTypePayloadTooLarge
	case http.StatusServiceUnavailable:
		return ProblemTypeServiceUnavailable
	case http.StatusInternalServerError:
		return ProblemTypeInternalServerError
	default:
		if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
			return ProblemTypeClientError
		}

		return ProblemTypeInternalServerError
	}
}
