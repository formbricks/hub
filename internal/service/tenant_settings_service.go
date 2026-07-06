package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// TenantSettingsRepository is the persistence surface the settings service needs.
type TenantSettingsRepository interface {
	Get(ctx context.Context, tenantID string) (*models.TenantSettings, bool, error)
	Upsert(ctx context.Context, tenantID string, settings models.EnrichmentSettings) (*models.TenantSettings, error)
	// Patch merges set into the tenant's settings and removes removeKeys (an RFC
	// 7396 merge patch: set + delete); the two are disjoint.
	Patch(
		ctx context.Context, tenantID string, set models.EnrichmentSettings, removeKeys []string,
	) (*models.TenantSettings, error)
}

// settingKeyTargetLanguage is the JSONB key for the target language. It must match
// the json tag on models.EnrichmentSettings.TargetLanguage; it is the key removed
// when a PATCH sends target_language as null. TestSettingKeyMatchesModelTag pins
// this to the tag so a rename can't silently break null-removal.
const settingKeyTargetLanguage = "target_language"

// settingKeySentimentEnabled is the JSONB key for the per-directory sentiment switch. It must
// match the json tag on models.EnrichmentSettings.SentimentEnabled; it is the key removed when a
// PATCH sends sentiment_enabled as null. Pinned to the tag by TestSettingKeyMatchesModelTag.
const settingKeySentimentEnabled = "sentiment_enabled"

// settingKeyEmotionsEnabled is the JSONB key for the per-directory emotion switch. It must match
// the json tag on models.EnrichmentSettings.EmotionsEnabled; it is the key removed when a PATCH
// sends an explicit null.
const settingKeyEmotionsEnabled = "emotions_enabled"

// maxTargetLanguageLen bounds a provided target_language value. It mirrors the
// `max=35` struct tag on UpdateTenantSettingsRequest (the PUT path) and the
// OpenAPI maxLength, so PUT and PATCH enforce the same limit.
const maxTargetLanguageLen = 35

// TenantSettingsService reads and writes tenant-scoped enrichment settings. It is
// the accessor enrichment workflows will use to resolve a tenant's configuration.
type TenantSettingsService struct {
	repo     TenantSettingsRepository
	listener SettingsChangeListener // optional; set via SetSettingsChangeListener
}

// NewTenantSettingsService creates a new tenant settings service.
func NewTenantSettingsService(repo TenantSettingsRepository) *TenantSettingsService {
	return &TenantSettingsService{repo: repo}
}

// SetSettingsChangeListener registers a listener notified after a successful settings write,
// used to trigger enrichment side-effects (e.g. a re-translation backfill on a
// target_language change). Optional; mirrors the post-construction injection of
// SetEmbeddingInserter. Nil means no side-effects fire.
func (s *TenantSettingsService) SetSettingsChangeListener(listener SettingsChangeListener) {
	s.listener = listener
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
		// Unconfigured tenant: zero-value (empty) settings, never a not-found error.
		return &models.TenantSettings{TenantID: normalizedTenantID}, nil
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

	settings, err := s.repo.Upsert(ctx, normalizedTenantID, models.EnrichmentSettings{
		TargetLanguage:   targetLanguage,
		SentimentEnabled: req.SentimentEnabled,
		EmotionsEnabled:  req.EmotionsEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("update tenant settings: %w", err)
	}

	// PUT is a full replace, so every settable key is (re)written.
	s.notifyChanged(ctx, normalizedTenantID,
		[]string{settingKeyTargetLanguage, settingKeySentimentEnabled, settingKeyEmotionsEnabled})

	return settings, nil
}

// PatchSettings applies an RFC 7396 JSON Merge Patch: a member present with a
// value sets that setting (validated and normalized), a member present with JSON
// null removes it, and an omitted member is left unchanged. It translates the
// typed request into the keys to set and the keys to remove, which are disjoint.
// The tenant_id comes from the request path and scopes the write to that tenant
// alone.
func (s *TenantSettingsService) PatchSettings(
	ctx context.Context, tenantID string, req *models.PatchTenantSettingsRequest,
) (*models.TenantSettings, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	var (
		set         models.EnrichmentSettings
		removeKeys  []string
		changedKeys []string
	)

	if req.TargetLanguage.Present {
		changedKeys = append(changedKeys, settingKeyTargetLanguage)

		if req.TargetLanguage.Value == nil {
			// Explicit null: remove the setting (RFC 7396).
			removeKeys = append(removeKeys, settingKeyTargetLanguage)
		} else {
			normalized, normErr := normalizeProvidedTargetLanguage(*req.TargetLanguage.Value)
			if normErr != nil {
				return nil, normErr
			}

			set.TargetLanguage = normalized
		}
	}

	if req.SentimentEnabled.Present {
		changedKeys = append(changedKeys, settingKeySentimentEnabled)

		if req.SentimentEnabled.Value == nil {
			// Explicit null: remove the setting, restoring the default (enabled) (RFC 7396).
			removeKeys = append(removeKeys, settingKeySentimentEnabled)
		} else {
			set.SentimentEnabled = req.SentimentEnabled.Value
		}
	}

	if req.EmotionsEnabled.Present {
		changedKeys = append(changedKeys, settingKeyEmotionsEnabled)

		if req.EmotionsEnabled.Value == nil {
			// Explicit null: remove the setting, restoring the default (enabled) (RFC 7396).
			removeKeys = append(removeKeys, settingKeyEmotionsEnabled)
		} else {
			set.EmotionsEnabled = req.EmotionsEnabled.Value
		}
	}

	settings, err := s.repo.Patch(ctx, normalizedTenantID, set, removeKeys)
	if err != nil {
		return nil, fmt.Errorf("patch tenant settings: %w", err)
	}

	// Fire only for keys the patch actually touched (an omitted member does not trigger).
	s.notifyChanged(ctx, normalizedTenantID, changedKeys)

	return settings, nil
}

// notifyChanged tells the registered listener which setting keys a write touched. No-op when
// no listener is set or nothing changed.
func (s *TenantSettingsService) notifyChanged(ctx context.Context, tenantID string, changedKeys []string) {
	if s.listener == nil || len(changedKeys) == 0 {
		return
	}

	s.listener.OnSettingsChanged(ctx, tenantID, changedKeys)
}

// normalizeTargetLanguage trims and canonicalizes a BCP-47 locale (e.g. "en-us"
// -> "en-US"). An empty value is allowed and normalizes to "" (target language
// not configured) — this is the PUT full-replace semantics, where omitting the
// field clears it. A malformed locale is rejected with a validation error.
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

// normalizeProvidedTargetLanguage validates a non-null target_language supplied in
// a PATCH. Unlike PUT, an empty value is rejected: under RFC 7396 the way to
// remove a setting is JSON null, so an explicit "" is a malformed locale rather
// than a clear. It also enforces the same null-byte and length bounds the PUT path
// gets from struct tags, which the Optional[string] field cannot carry.
func normalizeProvidedTargetLanguage(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", huberrors.NewValidationError(
			"target_language",
			"target_language must be a valid BCP-47 locale (e.g. en-US); send null to remove it")
	}

	if strings.ContainsRune(raw, '\x00') {
		return "", huberrors.NewValidationError(
			"target_language", "target_language must not contain null bytes")
	}

	if utf8.RuneCountInString(raw) > maxTargetLanguageLen {
		return "", huberrors.NewValidationError(
			"target_language",
			fmt.Sprintf("target_language must be at most %d characters", maxTargetLanguageLen))
	}

	return normalizeTargetLanguage(raw)
}
