package middleware

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/formbricks/hub/internal/api/response"
)

// errRequestBodyTooLarge is the message returned by http.MaxBytesReader when limit is exceeded.
const errRequestBodyTooLarge = "http: request body too large"

// mayHaveBody is true for methods that typically send a request body (we buffer only then to send 413).
func mayHaveBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

// RequestBodyTooLargeRecorder records when a request is rejected for exceeding the body limit (optional).
// Pass nil when metrics are disabled.
type RequestBodyTooLargeRecorder interface {
	RecordRequestBodyTooLarge(ctx context.Context)
}

// MaxBody returns a middleware that limits request body size to maxBytes.
// When the body exceeds the limit, the response is 413 Request Entity Too Large.
// recorder is optional; when non-nil, it is called for each rejected request.
// Use 0 or negative to disable (no limit); typically use config.MaxRequestBodyBytes.
func MaxBody(maxBytes int64, recorder RequestBodyTooLargeRecorder) func(http.Handler) http.Handler {
	if maxBytes <= 0 {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap body so we can detect when limit is exceeded (handler may then return 400/500).
			limited := http.MaxBytesReader(w, r.Body, maxBytes)

			var limitExceeded bool

			r.Body = &maxBodyReader{
				ReadCloser: limited,
				onReadError: func(err error) {
					if err != nil && strings.Contains(err.Error(), errRequestBodyTooLarge) {
						limitExceeded = true
					}
				},
			}

			// Only buffer response for methods that typically have a body, so we can send 413 when
			// limit is exceeded. GET/DELETE stream directly to avoid memory and TTFB cost.
			if mayHaveBody(r.Method) {
				buf := &responseBuffer{ResponseWriter: w}
				next.ServeHTTP(buf, r)

				if limitExceeded {
					if recorder != nil {
						recorder.RecordRequestBodyTooLarge(r.Context())
					}

					response.RespondError(buf.ResponseWriter, http.StatusRequestEntityTooLarge,
						"Request Entity Too Large", "request body exceeds maximum allowed size")

					return
				}

				buf.flush()

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type maxBodyReader struct {
	io.ReadCloser

	onReadError func(error)
}

func (r *maxBodyReader) Read(p []byte) (n int, err error) {
	n, err = r.ReadCloser.Read(p)
	if err != nil && r.onReadError != nil {
		r.onReadError(err)
	}

	if err != nil {
		return n, fmt.Errorf("read body: %w", err)
	}

	return n, nil
}

// responseBuffer captures status and body so we can optionally discard and send 413 instead.
type responseBuffer struct {
	http.ResponseWriter

	status int
	buf    bytes.Buffer
}

func (b *responseBuffer) WriteHeader(code int) {
	b.status = code
}

func (b *responseBuffer) Write(p []byte) (n int, err error) {
	n, err = b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("buffer write: %w", err)
	}

	return n, nil
}

func (b *responseBuffer) flush() {
	if b.status != 0 {
		b.ResponseWriter.WriteHeader(b.status)
	}

	_, _ = b.buf.WriteTo(b.ResponseWriter)
}
