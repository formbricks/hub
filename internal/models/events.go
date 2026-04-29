package models

import "github.com/google/uuid"

// DeletedIDsEventData is the tenant-aware payload for resource deletion events.
type DeletedIDsEventData struct {
	TenantID string      `json:"tenant_id"`
	IDs      []uuid.UUID `json:"ids"`
}
