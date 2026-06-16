package service

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/formbricks/hub/internal/huberrors"
)

const (
	maxIdentifierLength = 255
	maxTenantIDLength   = maxIdentifierLength
	maxUserIDLength     = maxIdentifierLength
)

func normalizeRequiredTenantID(tenantID *string) (string, error) {
	if tenantID == nil {
		return "", huberrors.NewValidationError("tenant_id", "tenant_id is required")
	}

	return normalizeRequiredTenantIDValue(*tenantID)
}

func normalizeRequiredTenantIDValue(tenantID string) (string, error) {
	return normalizeRequiredIdentifier("tenant_id", tenantID)
}

func normalizeRequiredUserIDValue(userID string) (string, error) {
	return normalizeRequiredIdentifier("user_id", userID)
}

func normalizeRequiredIdentifier(fieldName, value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", huberrors.NewValidationError(fieldName, fieldName+" is required and cannot be empty")
	}

	return normalizeIdentifierValue(fieldName, normalized)
}

// normalizeOptionalIdentifier trims an identifier and validates it when present.
// An empty (or blank) value is allowed and normalizes to "", which callers treat as the
// canonical "no value" bucket (e.g. taxonomy's "no source" scope).
func normalizeOptionalIdentifier(fieldName, value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", nil
	}

	return normalizeIdentifierValue(fieldName, normalized)
}

func normalizeIdentifierValue(fieldName, normalized string) (string, error) {
	if strings.ContainsRune(normalized, '\x00') {
		return "", huberrors.NewValidationError(fieldName, fieldName+" must not contain NULL bytes")
	}

	if utf8.RuneCountInString(normalized) > maxIdentifierLength {
		return "", huberrors.NewValidationError(fieldName, fieldName+" must be at most "+strconv.Itoa(maxIdentifierLength)+" characters")
	}

	return normalized, nil
}
