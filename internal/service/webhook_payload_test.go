package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
)

func TestNewWebhookPayload_MapsDeletedIDsEventDataToPublicPayload(t *testing.T) {
	tenantID := "org-123"
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	args := WebhookDispatchArgs{
		EventID:   uuid.Must(uuid.NewV7()),
		EventType: "feedback_record.deleted",
		Timestamp: time.Now(),
		TenantID:  &tenantID,
		Data:      models.DeletedIDsEventData{TenantID: tenantID, IDs: ids},
		WebhookID: uuid.Must(uuid.NewV7()),
	}

	payload := NewWebhookPayload(args)

	if payload.TenantID == nil || *payload.TenantID != tenantID {
		t.Fatalf("TenantID = %v, want %q", payload.TenantID, tenantID)
	}

	gotIDs, ok := payload.Data.([]uuid.UUID)
	if !ok {
		t.Fatalf("Data type = %T, want []uuid.UUID", payload.Data)
	}

	if len(gotIDs) != len(ids) {
		t.Fatalf("Data length = %d, want %d", len(gotIDs), len(ids))
	}

	for i := range ids {
		if gotIDs[i] != ids[i] {
			t.Errorf("Data[%d] = %v, want %v", i, gotIDs[i], ids[i])
		}
	}

	ids[0] = uuid.Must(uuid.NewV7())
	if gotIDs[0] == ids[0] {
		t.Error("Data aliases internal deleted ID slice")
	}
}

func TestNewWebhookPayload_MapsJSONRoundTrippedDeletedIDsEventDataToPublicPayload(t *testing.T) {
	tenantID := "org-123"
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	args := WebhookDispatchArgs{
		EventID:   uuid.Must(uuid.NewV7()),
		EventType: "feedback_record.deleted",
		Timestamp: time.Now(),
		Data: map[string]any{
			"tenant_id": tenantID,
			"ids":       []any{ids[0].String(), ids[1].String()},
		},
		WebhookID: uuid.Must(uuid.NewV7()),
	}

	payload := NewWebhookPayload(args)

	if payload.TenantID == nil || *payload.TenantID != tenantID {
		t.Fatalf("TenantID = %v, want %q", payload.TenantID, tenantID)
	}

	assertWebhookPayloadIDs(t, payload.Data, ids)
}

func TestNewWebhookPayload_MapsRawJSONDeletedIDsEventDataToPublicPayload(t *testing.T) {
	tenantID := "org-123"
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	args := WebhookDispatchArgs{
		EventID:   uuid.Must(uuid.NewV7()),
		EventType: "webhook.deleted",
		Timestamp: time.Now(),
		Data: json.RawMessage(`{
			"tenant_id": "` + tenantID + `",
			"ids": ["` + ids[0].String() + `", "` + ids[1].String() + `"]
		}`),
		WebhookID: uuid.Must(uuid.NewV7()),
	}

	payload := NewWebhookPayload(args)

	if payload.TenantID == nil || *payload.TenantID != tenantID {
		t.Fatalf("TenantID = %v, want %q", payload.TenantID, tenantID)
	}

	assertWebhookPayloadIDs(t, payload.Data, ids)
}

func TestNewWebhookPayload_DerivesTenantFromLegacyArgsData(t *testing.T) {
	tenantID := "org-123"
	args := WebhookDispatchArgs{
		EventID:   uuid.Must(uuid.NewV7()),
		EventType: "feedback_record.created",
		Timestamp: time.Now(),
		Data:      map[string]any{"tenant_id": tenantID},
		WebhookID: uuid.Must(uuid.NewV7()),
	}

	payload := NewWebhookPayload(args)

	if payload.TenantID == nil || *payload.TenantID != tenantID {
		t.Fatalf("TenantID = %v, want %q", payload.TenantID, tenantID)
	}
}

func assertWebhookPayloadIDs(t *testing.T, data any, want []uuid.UUID) {
	t.Helper()

	got, ok := data.([]uuid.UUID)
	if !ok {
		t.Fatalf("Data type = %T, want []uuid.UUID", data)
	}

	if len(got) != len(want) {
		t.Fatalf("Data length = %d, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Data[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
