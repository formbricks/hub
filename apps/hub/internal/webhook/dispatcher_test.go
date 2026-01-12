package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDispatcher_Dispatch_Success(t *testing.T) {
	done := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()

		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}
		if ua := r.Header.Get("User-Agent"); ua != "Formbricks-Hub/1.0" {
			t.Errorf("expected User-Agent Formbricks-Hub/1.0, got %s", ua)
		}

		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}

		if event.Event != EventFeedbackRecordCreated {
			t.Errorf("expected event %q, got %q", EventFeedbackRecordCreated, event.Event)
		}
		if event.Timestamp.IsZero() {
			t.Error("expected timestamp to be set")
		}

		payload, ok := event.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("expected payload to be a map, got %T", event.Data)
		}
		if payload["id"] == "" {
			t.Error("expected payload to contain id")
		}

		w.WriteHeader(http.StatusOK)
		close(done)
	}))
	defer server.Close()

	dispatcher := NewDispatcher([]string{server.URL}, newTestLogger())
	dispatcher.client = server.Client()

	dispatcher.Dispatch(context.Background(), EventFeedbackRecordCreated, map[string]interface{}{
		"id":          uuid.NewString(),
		"source_type": "survey",
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for webhook dispatch")
	}
}

func TestDispatcher_Dispatch_Retry(t *testing.T) {
	var attempts atomic.Int32
	done := make(chan struct{})

	dispatcher := NewDispatcher([]string{"http://example.com/webhook"}, newTestLogger())
	dispatcher.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			current := attempts.Add(1)
			if current < 3 {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(bytes.NewBuffer(nil)),
				}, nil
			}

			select {
			case done <- struct{}{}:
			default:
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(nil)),
			}, nil
		}),
	}

	dispatcher.Dispatch(context.Background(), EventFeedbackRecordCreated, map[string]any{
		"id": uuid.NewString(),
	})

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("expected dispatcher to succeed after retries")
	}

	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestDispatcher_Dispatch_NoWebhooks(t *testing.T) {
	dispatcher := NewDispatcher(nil, newTestLogger())

	done := make(chan struct{})
	go func() {
		dispatcher.Dispatch(context.Background(), EventFeedbackRecordCreated, map[string]any{
			"id": uuid.NewString(),
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dispatcher did not return immediately with no webhooks configured")
	}
}
