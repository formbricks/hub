package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

// enrichmentStatusQueryTimeout bounds the settings lookup + count query for one request. The
// endpoint is polled and there is no HTTP handler timeout, so a slow count must not pin a DB
// pool connection indefinitely.
const enrichmentStatusQueryTimeout = 5 * time.Second

// EnrichmentStatusRepository is the count surface the status service needs.
type EnrichmentStatusRepository interface {
	CountEnrichmentStatus(ctx context.Context, tenantID, defaultLang string) (repository.EnrichmentStatusCounts, error)
}

// EnrichmentStatusService reports a tenant's enrichment progress. It resolves the per-tenant
// enable state from settings (via TenantSettingsReader) and overlays the deployment-level
// (provider/model) gate; the repo supplies the counts.
type EnrichmentStatusService struct {
	repo                  EnrichmentStatusRepository
	settings              TenantSettingsReader
	defaultLang           string
	translationConfigured bool
	sentimentConfigured   bool
	emotionsConfigured    bool
}

// NewEnrichmentStatusServiceParams configures an EnrichmentStatusService. The *Configured flags
// are the deployment-level gates (provider+model set), mirroring how the enrichment providers are
// constructed; defaultLang is TRANSLATION_DEFAULT_LANGUAGE.
type NewEnrichmentStatusServiceParams struct {
	Repo                  EnrichmentStatusRepository
	Settings              TenantSettingsReader
	DefaultLang           string
	TranslationConfigured bool
	SentimentConfigured   bool
	EmotionsConfigured    bool
}

// NewEnrichmentStatusService creates an enrichment status service.
func NewEnrichmentStatusService(params NewEnrichmentStatusServiceParams) *EnrichmentStatusService {
	return &EnrichmentStatusService{
		repo:                  params.Repo,
		settings:              params.Settings,
		defaultLang:           strings.TrimSpace(params.DefaultLang),
		translationConfigured: params.TranslationConfigured,
		sentimentConfigured:   params.SentimentConfigured,
		emotionsConfigured:    params.EmotionsConfigured,
	}
}

// GetEnrichmentStatus returns the tenant's per-enrichment progress. tenant_id is required and
// validated; the query is scoped to that tenant alone.
func (s *EnrichmentStatusService) GetEnrichmentStatus(
	ctx context.Context, tenantID string,
) (*models.EnrichmentStatusResponse, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, enrichmentStatusQueryTimeout)
	defer cancel()

	settings, err := s.settings.GetSettings(ctx, normalizedTenantID)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant settings: %w", err)
	}

	counts, err := s.repo.CountEnrichmentStatus(ctx, normalizedTenantID, s.defaultLang)
	if err != nil {
		return nil, fmt.Errorf("count enrichment status: %w", err)
	}

	translationEnabled := s.translationConfigured &&
		resolveTargetLang(settings.Settings.TargetLanguage, s.defaultLang) != ""

	return &models.EnrichmentStatusResponse{
		TenantID: normalizedTenantID,
		Translation: enrichmentTypeStatus(translationEnabled,
			counts.TranslationEligible, counts.TranslationDone),
		Sentiment: enrichmentTypeStatus(s.sentimentConfigured && settings.Settings.SentimentEnrichmentEnabled(),
			counts.SentimentEligible, counts.SentimentDone),
		Emotions: enrichmentTypeStatus(s.emotionsConfigured && settings.Settings.EmotionsEnrichmentEnabled(),
			counts.EmotionsEligible, counts.EmotionsDone),
	}, nil
}

// enrichmentTypeStatus assembles one enrichment's status, zeroing the counts when it is not
// enabled for the tenant so the API never reports a backlog for work that will never run.
func enrichmentTypeStatus(enabled bool, eligible, done int64) models.EnrichmentTypeStatus {
	if !enabled {
		return models.EnrichmentTypeStatus{Enabled: false}
	}

	return models.EnrichmentTypeStatus{Enabled: true, Eligible: eligible, Done: done}
}
