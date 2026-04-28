package service

import (
	"encoding/json"
	"testing"

	"github.com/formbricks/hub/internal/models"
)

func TestTenantIDFromEventData(t *testing.T) {
	tenantID := "org-123"

	tests := []struct {
		name string
		data any
		want string
		ok   bool
	}{
		{
			name: "feedback record pointer",
			data: &models.FeedbackRecord{TenantID: tenantID},
			want: tenantID,
			ok:   true,
		},
		{
			name: "feedback record value",
			data: models.FeedbackRecord{TenantID: tenantID},
			want: tenantID,
			ok:   true,
		},
		{
			name: "webhook pointer",
			data: &models.Webhook{TenantID: &tenantID},
			want: tenantID,
			ok:   true,
		},
		{
			name: "deleted IDs event data",
			data: models.DeletedIDsEventData{TenantID: tenantID},
			want: tenantID,
			ok:   true,
		},
		{
			name: "map any",
			data: map[string]any{"tenant_id": tenantID},
			want: tenantID,
			ok:   true,
		},
		{
			name: "map string",
			data: map[string]string{"tenant_id": tenantID},
			want: tenantID,
			ok:   true,
		},
		{
			name: "raw json",
			data: json.RawMessage(`{"tenant_id":"org-123"}`),
			want: tenantID,
			ok:   true,
		},
		{
			name: "struct with json tag fallback",
			data: struct {
				TenantID string `json:"tenant_id"`
			}{TenantID: tenantID},
			want: tenantID,
			ok:   true,
		},
		{
			name: "tenant-less data",
			data: []string{"record-id"},
			ok:   false,
		},
		{
			name: "empty tenant",
			data: map[string]any{"tenant_id": ""},
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := TenantIDFromEventData(tt.data)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}

			if got != tt.want {
				t.Errorf("tenantID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWebhookMatchesTenant(t *testing.T) {
	tenantID := "org-123"
	otherTenantID := "org-other"

	tests := []struct {
		name     string
		webhook  *models.Webhook
		tenantID *string
		want     bool
	}{
		{name: "webhook without tenant rejects tenant event", webhook: &models.Webhook{}, tenantID: &tenantID, want: false},
		{name: "webhook without tenant rejects tenant-less event", webhook: &models.Webhook{}, tenantID: nil, want: false},
		{name: "scoped webhook matches same tenant", webhook: &models.Webhook{TenantID: &tenantID}, tenantID: &tenantID, want: true},
		{name: "scoped webhook rejects different tenant", webhook: &models.Webhook{TenantID: &tenantID}, tenantID: &otherTenantID, want: false},
		{name: "scoped webhook rejects tenant-less event", webhook: &models.Webhook{TenantID: &tenantID}, tenantID: nil, want: false},
		{name: "nil webhook rejects", webhook: nil, tenantID: &tenantID, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WebhookMatchesTenant(tt.webhook, tt.tenantID)
			if got != tt.want {
				t.Errorf("WebhookMatchesTenant() = %v, want %v", got, tt.want)
			}
		})
	}
}
