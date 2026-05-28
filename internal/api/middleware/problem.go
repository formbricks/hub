package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/formbricks/hub/internal/api/response"
)

// ProblemErrors normalizes the standard library ServeMux's plain-text 404 and
// 405 responses into RFC 9457 problem+json, so every error the API emits shares
// one shape and is logged once. Handlers that already write problem+json
// (their own 404/405) set the Content-Type before WriteHeader and are passed
// through untouched. Any Allow header ServeMux set for a 405 is preserved.
func ProblemErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pw := &problemErrorWriter{ResponseWriter: w, r: r}
		next.ServeHTTP(pw, r)
	})
}

// problemErrorWriter rewrites a default plain-text 404/405 into a problem
// response. It decides at WriteHeader time (before any body is written) so
// successful responses are never buffered.
type problemErrorWriter struct {
	http.ResponseWriter

	r           *http.Request
	wroteHeader bool
	intercepted bool
}

// Unwrap exposes the wrapped ResponseWriter so http.NewResponseController can
// traverse the middleware chain to reach optional interfaces (Flusher,
// Hijacker, Pusher, ReaderFrom). Handlers needing streaming or upgrades should
// use NewResponseController(w) rather than a direct type assertion on w.
func (w *problemErrorWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *problemErrorWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}

	w.wroteHeader = true

	if w.shouldIntercept(code) {
		w.intercepted = true
		response.RespondProblem(w.ResponseWriter, w.r, code, problemErrorDetail(code))

		return
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *problemErrorWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	// Swallow the standard library's plain-text body; the problem response was
	// already written from WriteHeader.
	if w.intercepted {
		return len(data), nil
	}

	n, err := w.ResponseWriter.Write(data)
	if err != nil {
		return n, fmt.Errorf("write response: %w", err)
	}

	return n, nil
}

// shouldIntercept reports whether code is a routing-level 404/405 that the
// standard library wrote as plain text (i.e. not already problem+json).
func (w *problemErrorWriter) shouldIntercept(code int) bool {
	if code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		return false
	}

	return !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json")
}

func problemErrorDetail(code int) string {
	if code == http.StatusMethodNotAllowed {
		return "The HTTP method is not allowed for this resource"
	}

	return "The requested resource was not found"
}
