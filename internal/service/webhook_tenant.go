package service

import (
	"encoding/json"

	"github.com/formbricks/hub/internal/models"
)

// TenantIDFromEventData extracts tenant_id from known event payload shapes.
func TenantIDFromEventData(data any) (string, bool) {
	switch payload := data.(type) {
	case *models.FeedbackRecord:
		if payload == nil {
			return "", false
		}

		return tenantIDFromString(payload.TenantID)
	case models.FeedbackRecord:
		return tenantIDFromString(payload.TenantID)
	case *models.Webhook:
		if payload == nil {
			return "", false
		}

		return tenantIDFromPointer(payload.TenantID)
	case models.Webhook:
		return tenantIDFromPointer(payload.TenantID)
	case *models.WebhookPublic:
		if payload == nil {
			return "", false
		}

		return tenantIDFromPointer(payload.TenantID)
	case models.WebhookPublic:
		return tenantIDFromPointer(payload.TenantID)
	case map[string]any:
		return tenantIDFromMapValue(payload["tenant_id"])
	case map[string]string:
		return tenantIDFromString(payload["tenant_id"])
	case json.RawMessage:
		return tenantIDFromRawJSON(payload)
	}

	return tenantIDFromJSON(data)
}

// TenantIDPointerFromEventData returns a detached pointer so it can be safely stored in job args.
func TenantIDPointerFromEventData(data any) *string {
	tenantID, ok := TenantIDFromEventData(data)
	if !ok {
		return nil
	}

	return stringPointer(tenantID)
}

// WebhookMatchesTenant reports whether a webhook may receive an event with tenantID.
func WebhookMatchesTenant(webhook *models.Webhook, tenantID *string) bool {
	if webhook == nil {
		return false
	}

	if webhook.TenantID == nil {
		return true
	}

	if tenantID == nil {
		return false
	}

	return *webhook.TenantID == *tenantID
}

func tenantIDFromMapValue(value any) (string, bool) {
	tenantID, ok := value.(string)
	if !ok {
		return "", false
	}

	return tenantIDFromString(tenantID)
}

func tenantIDFromPointer(tenantID *string) (string, bool) {
	if tenantID == nil {
		return "", false
	}

	return tenantIDFromString(*tenantID)
}

func tenantIDFromString(tenantID string) (string, bool) {
	if tenantID == "" {
		return "", false
	}

	return tenantID, true
}

func tenantIDFromJSON(data any) (string, bool) {
	payload, err := json.Marshal(data)
	if err != nil {
		return "", false
	}

	return tenantIDFromRawJSON(payload)
}

func tenantIDFromRawJSON(payload []byte) (string, bool) {
	var envelope struct {
		TenantID *string `json:"tenant_id"`
	}

	if err := json.Unmarshal(payload, &envelope); err != nil {
		return "", false
	}

	return tenantIDFromPointer(envelope.TenantID)
}

func stringPointer(value string) *string {
	v := value

	return &v
}
