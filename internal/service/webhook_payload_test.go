package service

import (
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
