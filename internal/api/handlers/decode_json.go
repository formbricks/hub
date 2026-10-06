package handlers

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
)

// maxSmallJSONBodyBytes caps request bodies whose largest valid payload is a few kilobytes
// (semantic search, webhooks, taxonomy run requests). They had no cap before ENG-3658; this is
// generous headroom over their bounded fields while still refusing multi-megabyte bodies before
// they are buffered.
const maxSmallJSONBodyBytes = 64 << 10

// maxPresizeBytes bounds how much a request's Content-Length can make readBoundedBody reserve up
// front. It covers every cap except taxonomy results, which grow past it as bytes actually arrive.
const maxPresizeBytes = 1 << 20

// jsonWhitespace is the insignificant whitespace RFC 8259 allows around a value. bytes.TrimSpace
// would also strip Unicode spaces such as U+00A0, which are not JSON whitespace.
const jsonWhitespace = " \t\r\n"

// decodeJSONBody reads a request body of at most maxBytes and decodes it into dst with
// encoding/json/v2, refusing unknown members (ENG-3658).
//
// encoding/json (v1) matched member names case-insensitively, kept the last of a repeated member
// and stopped after the first top-level value — so `{"tenant_id":"A","TENANT_ID":"B"}` decoded as
// tenant B while a gateway that read the same bytes exactly had authorized tenant A. v2's defaults
// close all of that: names match exactly, a repeated name anywhere in the body is a syntax error,
// and so is anything after the top-level value. Types with custom decoding implement
// json.UnmarshalerFrom, so the same options reach their members too.
//
// Invalid UTF-8 in string values is deliberately still allowed and mangled to U+FFFD, as v1 did:
// the request validators own that class (storable_json names `metadata` for ENG-2745), and v2's
// default would refuse it earlier as a bare syntax error. It cannot reopen the member-name
// problem — a name holding U+FFFD matches no field and is refused as unknown.
//
// Every error is a *response.RequestJSONDecodeError, so response.RespondError maps it: 413 for an
// oversized body, 400 otherwise. An empty or whitespace-only body wraps io.EOF, which callers that
// accept an absent body can test for with errors.Is. A JSON null decodes as the zero value, as it
// always has.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if err := decodeStrictJSONBody(w, r, dst, maxBytes); err != nil {
		//nolint:wrapcheck // NewRequestJSONDecodeError is the wrapper: it marks a request-body failure for the response mapping.
		return response.NewRequestJSONDecodeError(err)
	}

	return nil
}

// decodeStrictJSONBody does decodeJSONBody's work and returns the unwrapped cause.
func decodeStrictJSONBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	body, err := readBoundedBody(w, r, maxBytes)
	if err != nil {
		return err
	}

	if len(bytes.Trim(body, jsonWhitespace)) == 0 {
		return io.EOF
	}

	//nolint:wrapcheck // Mapped by type in the response package; wrapping would change nothing it reads.
	return json.Unmarshal(body, dst, requestBodyOptions)
}

// requestBodyOptions are the decode options for every request body:
//
//   - RejectUnknownMembers, so a member that does not exactly match a field is refused;
//   - AllowInvalidUTF8 keeps v1's handling of invalid UTF-8 in values (see decodeJSONBody);
//   - time.Time keeps v1's parsing. v2's own format is strict RFC 3339, which would refuse timestamps
//     clients already send successfully (a single-digit hour, a comma before the fraction) — an
//     ingestion change unrelated to member names.
var requestBodyOptions = json.JoinOptions(
	json.RejectUnknownMembers(true),
	jsontext.AllowInvalidUTF8(true),
	json.WithUnmarshalers(json.UnmarshalFunc(func(data []byte, t *time.Time) error {
		// Returned as is: v2 wraps it with the member's position.
		return jsonv1.Unmarshal(data, t)
	})),
)

// decodeAndValidateJSONBody decodes like decodeJSONBody, then validates dst's struct tags. It writes
// the matching problem response itself and reports false when it has, so callers just `return`.
func decodeAndValidateJSONBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	if err := decodeJSONBody(w, r, dst, maxBytes); err != nil {
		response.RespondError(w, r, err)

		return false
	}

	if err := validation.ValidateStruct(dst); err != nil {
		response.RespondError(w, r, err)

		return false
	}

	return true
}

// readBoundedBody reads the whole body through http.MaxBytesReader, so an oversized body fails
// with *http.MaxBytesError before more than maxBytes is buffered.
func readBoundedBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	// Pre-size from Content-Length when it is within the cap, so a body is read without regrowing;
	// ReadFrom wants bytes.MinRead of spare room to observe EOF. Bounded by maxPresizeBytes, because
	// the header is the caller's claim: it must not make the server reserve a large cap (16 MiB on
	// taxonomy results) before a byte has arrived.
	var buf bytes.Buffer
	if r.ContentLength > 0 && r.ContentLength <= maxBytes {
		buf.Grow(int(min(r.ContentLength, maxPresizeBytes)) + bytes.MinRead)
	}

	if _, err := buf.ReadFrom(r.Body); err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}

	return buf.Bytes(), nil
}
