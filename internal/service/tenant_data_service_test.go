package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockTenantDataRepo struct {
	tenantID string
	counts   *models.TenantDataDeleteCounts
	err      error
}

func (m *mockTenantDataRepo) DeleteByTenant(
	_ context.Context, tenantID string,
) (*models.TenantDataDeleteCounts, error) {
	m.tenantID = tenantID

	return m.counts, m.err
}

func TestTenantDataService_DeleteTenantData(t *testing.T) {
	t.Run("normalizes tenant id and returns counts", func(t *testing.T) {
		repo := &mockTenantDataRepo{
			counts: &models.TenantDataDeleteCounts{
				DeletedFeedbackRecords: 4,
				DeletedEmbeddings:      3,
				DeletedWebhooks:        2,
			},
		}
		svc := NewTenantDataService(repo)

		result, err := svc.DeleteTenantData(context.Background(), " org-123 ")
		if err != nil {
			t.Fatalf("DeleteTenantData() error = %v", err)
		}

		if repo.tenantID != "org-123" {
			t.Fatalf("repo tenantID = %q, want org-123", repo.tenantID)
		}

		if result.TenantID != "org-123" {
			t.Fatalf("result TenantID = %q, want org-123", result.TenantID)
		}

		if result.DeletedFeedbackRecords != 4 || result.DeletedEmbeddings != 3 || result.DeletedWebhooks != 2 {
			t.Fatalf("result counts = %+v, want feedback=4 embeddings=3 webhooks=2", result.TenantDataDeleteCounts)
		}
	})

	t.Run("rejects invalid tenant id", func(t *testing.T) {
		repo := &mockTenantDataRepo{}
		svc := NewTenantDataService(repo)

		result, err := svc.DeleteTenantData(context.Background(), " \x00 ")
		if !errors.Is(err, huberrors.ErrValidation) {
			t.Fatalf("DeleteTenantData() error = %v, want validation", err)
		}

		if result != nil {
			t.Fatalf("result = %+v, want nil", result)
		}

		if repo.tenantID != "" {
			t.Fatalf("repo tenantID = %q, want no call", repo.tenantID)
		}
	})

	t.Run("wraps repository errors", func(t *testing.T) {
		repoErr := errors.New("repository failed")
		repo := &mockTenantDataRepo{err: repoErr}
		svc := NewTenantDataService(repo)

		result, err := svc.DeleteTenantData(context.Background(), "org-123")
		if !errors.Is(err, repoErr) {
			t.Fatalf("DeleteTenantData() error = %v, want wrapped repo error", err)
		}

		if result != nil {
			t.Fatalf("result = %+v, want nil", result)
		}
	})

	t.Run("returns error when repository returns nil counts", func(t *testing.T) {
		repo := &mockTenantDataRepo{}
		svc := NewTenantDataService(repo)

		result, err := svc.DeleteTenantData(context.Background(), "org-123")
		if err == nil {
			t.Fatal("DeleteTenantData() error = nil, want error")
		}

		if !strings.Contains(err.Error(), "repository returned nil counts") {
			t.Fatalf("DeleteTenantData() error = %v, want nil counts context", err)
		}

		if result != nil {
			t.Fatalf("result = %+v, want nil", result)
		}
	})
}

func TestNormalizeRequiredTenantIDValue(t *testing.T) {
	longTenantID := make([]rune, maxTenantIDLength+1)
	for i := range longTenantID {
		longTenantID[i] = 'a'
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "trims whitespace", input: " org-123 ", want: "org-123"},
		{name: "rejects empty", input: "   ", wantErr: true},
		{name: "rejects null byte", input: "org-\x00123", wantErr: true},
		{name: "rejects too long", input: string(longTenantID), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeRequiredTenantIDValue(tt.input)
			if tt.wantErr {
				if !errors.Is(err, huberrors.ErrValidation) {
					t.Fatalf("normalizeRequiredTenantIDValue() error = %v, want validation", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("normalizeRequiredTenantIDValue() error = %v", err)
			}

			if got != tt.want {
				t.Fatalf("normalizeRequiredTenantIDValue() = %q, want %q", got, tt.want)
			}
		})
	}
}
