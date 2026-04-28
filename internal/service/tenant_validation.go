package service

import (
	"strings"

	"github.com/formbricks/hub/internal/huberrors"
)

func normalizeRequiredTenantID(tenantID *string) (string, error) {
	if tenantID == nil {
		return "", huberrors.NewValidationError("tenant_id", "tenant_id is required")
	}

	normalized := strings.TrimSpace(*tenantID)
	if normalized == "" {
		return "", huberrors.NewValidationError("tenant_id", "tenant_id is required and cannot be empty")
	}

	return normalized, nil
}
