package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMaxBody_Disabled(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := MaxBody(0, nil)(handler)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("body"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("MaxBody(0) should pass through: got status %d", rec.Code)
	}
}

func TestMaxBody_NegativeDisables(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := MaxBody(-1, nil)(handler)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("body"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("MaxBody(-1) should pass through: got status %d", rec.Code)
	}
}

func TestMaxBody_POST_BodyUnderLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})
	mw := MaxBody(100, nil)(handler)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("small body"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("body under limit should pass: got status %d", rec.Code)
	}
}

func TestMaxBody_POST_BodyOverLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})
	mw := MaxBody(10, nil)(handler)
	body := bytes.Repeat([]byte("x"), 20)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("body over limit should return 413: got status %d", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("expected application/problem+json, got %q", ct)
	}
}

func TestMaxBody_GET_PassesThroughWithoutBuffering(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mw := MaxBody(10, nil)(handler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET should pass through: got status %d", rec.Code)
	}

	if rec.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", rec.Body.String())
	}
}

func TestMaxBody_DELETE_PassesThrough(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mw := MaxBody(10, nil)(handler)
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("DELETE should pass through: got status %d", rec.Code)
	}
}

func TestMaxBody_RecorderCalledWhenLimitExceeded(t *testing.T) {
	var recorded bool

	recorder := &mockRecorder{
		record: func() { recorded = true },
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})
	mw := MaxBody(10, recorder)(handler)
	body := bytes.Repeat([]byte("x"), 20)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413: got status %d", rec.Code)
	}

	if !recorded {
		t.Error("Recorder.RecordRequestBodyTooLarge should have been called")
	}
}

func TestMaxBody_RecorderNilWhenLimitExceeded(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})
	mw := MaxBody(10, nil)(handler)
	body := bytes.Repeat([]byte("x"), 20)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 when recorder is nil: got status %d", rec.Code)
	}
}

func TestMaxBody_PUT_OverLimitReturns413(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})
	mw := MaxBody(5, nil)(handler)
	body := bytes.Repeat([]byte("a"), 10)
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("PUT with over-limit body should return 413: got status %d", rec.Code)
	}
}

func TestMaxBody_PATCH_OverLimitReturns413(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})
	mw := MaxBody(5, nil)(handler)
	body := bytes.Repeat([]byte("a"), 10)
	req := httptest.NewRequest(http.MethodPatch, "/", bytes.NewBuffer(body))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("PATCH with over-limit body should return 413: got status %d", rec.Code)
	}
}

type mockRecorder struct {
	record func()
}

func (m *mockRecorder) RecordRequestBodyTooLarge(_ context.Context) {
	if m.record != nil {
		m.record()
	}
}
