package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/formbricks/hub/internal/observability"
)

func TestNewTaxonomyClientRequiresConfig(t *testing.T) {
	_, err := NewTaxonomyClient(TaxonomyClientConfig{}, nil)
	if !errors.Is(err, ErrTaxonomyServiceURLRequired) {
		t.Fatalf("NewTaxonomyClient() error = %v, want %v", err, ErrTaxonomyServiceURLRequired)
	}

	_, err = NewTaxonomyClient(TaxonomyClientConfig{ServiceURL: "https://taxonomy.test"}, nil)
	if !errors.Is(err, ErrTaxonomyServiceTokenRequired) {
		t.Fatalf("NewTaxonomyClient() error = %v, want %v", err, ErrTaxonomyServiceTokenRequired)
	}
}

func TestTaxonomyClientStartRunSendsBearerTokenAndRequestID(t *testing.T) {
	var (
		gotAuth      string
		gotRequestID string
		gotPath      string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotRequestID = r.Header.Get("X-Request-ID")
		gotPath = r.URL.Path

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client, err := NewTaxonomyClient(TaxonomyClientConfig{
		ServiceURL:   server.URL,
		ServiceToken: "taxonomy-service-token",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewTaxonomyClient() error = %v", err)
	}

	ctx := context.WithValue(context.Background(), observability.RequestIDKey, "request-1")
	if err := client.StartRun(ctx, "run-1"); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}

	if gotAuth != "Bearer taxonomy-service-token" {
		t.Fatalf("Authorization header = %q, want Bearer taxonomy-service-token", gotAuth)
	}

	if gotRequestID != "request-1" {
		t.Fatalf("X-Request-ID header = %q, want request-1", gotRequestID)
	}

	if gotPath != "/v1/runs/run-1/start" {
		t.Fatalf("path = %q, want /v1/runs/run-1/start", gotPath)
	}
}

func TestTaxonomyClientStartRunReturnsNon2xxError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := NewTaxonomyClient(TaxonomyClientConfig{
		ServiceURL:   server.URL,
		ServiceToken: "taxonomy-service-token",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewTaxonomyClient() error = %v", err)
	}

	err = client.StartRun(context.Background(), "run-1")
	if !errors.Is(err, ErrTaxonomyServiceUnexpectedStatus) {
		t.Fatalf("StartRun() error = %v, want %v", err, ErrTaxonomyServiceUnexpectedStatus)
	}
}
