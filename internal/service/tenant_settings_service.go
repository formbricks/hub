package service

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/text/language"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// TenantSettingsRepository is the persistence surface the settings service needs.
type TenantSettingsRepository interface {
	Get(ctx context.Context, tenantID string) (*models.TenantSettings, bool, error)
	Upsert(ctx context.Context, tenantID string, settings models.EnrichmentSettings) (*models.TenantSettings, error)
}

// TenantSettingsService reads and writes tenant-scoped enrichment settings. It is
// the accessor enrichment workflows use to resolve a tenant's configuration.
type TenantSettingsService struct {
	repo TenantSettingsRepository
}

// NewTenantSettingsService creates a new tenant settings service.
func NewTenantSettingsService(repo TenantSettingsRepository) *TenantSettingsService {
	return &TenantSettingsService{repo: repo}
}

// GetSettings returns the tenant's enrichment settings. When the tenant has no
// settings yet it returns a zero-value settings bag (target language unset)
// rather than a not-found error: an unconfigured tenant is a valid state, and
// consumers decide the fallback behavior. The lookup is always scoped to the
// normalized tenant_id.
func (s *TenantSettingsService) GetSettings(ctx context.Context, tenantID string) (*models.TenantSettings, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	settings, found, err := s.repo.Get(ctx, normalizedTenantID)
	if err != nil {
		return nil, fmt.Errorf("get tenant settings: %w", err)
	}

	if !found {
		return &models.TenantSettings{TenantID: normalizedTenantID, Settings: models.EnrichmentSettings{}}, nil
	}

	return settings, nil
}

// UpdateSettings validates and normalizes the requested settings, then upserts
// them for the tenant (full replace). The tenant_id is supplied by the caller
// (from the request path) and scopes the write to that tenant alone.
func (s *TenantSettingsService) UpdateSettings(
	ctx context.Context, tenantID string, req *models.UpdateTenantSettingsRequest,
) (*models.TenantSettings, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	targetLanguage, err := normalizeTargetLanguage(req.TargetLanguage)
	if err != nil {
		return nil, err
	}

	settings, err := s.repo.Upsert(ctx, normalizedTenantID, models.EnrichmentSettings{TargetLanguage: targetLanguage})
	if err != nil {
		return nil, fmt.Errorf("update tenant settings: %w", err)
	}

	return settings, nil
}

// normalizeTargetLanguage trims and canonicalizes a BCP-47 locale (e.g. "en-us"
// -> "en-US"). An empty value is allowed and normalizes to "" (target language
// not configured). A malformed locale is rejected with a validation error.
func normalizeTargetLanguage(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	tag, err := language.Parse(trimmed)
	if err != nil {
		return "", huberrors.NewValidationError(
			"target_language", "target_language must be a valid BCP-47 locale (e.g. en-US)")
	}

	return tag.String(), nil
}
