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

	// Stamped after the count returns, so AsOf describes when the numbers were true rather than
	// when the response happened to be written. UTC because it crosses a process boundary.
	asOf := time.Now().UTC()

	return &models.EnrichmentStatusResponse{
		TenantID: normalizedTenantID,
		AsOf:     asOf,
		Translation: enrichmentTypeStatus(
			translationDisabledReason(s.translationConfigured,
				resolveTargetLang(settings.Settings.TargetLanguage, s.defaultLang)),
			enrichmentCounts{
				eligible: counts.TranslationEligible, done: counts.TranslationDone,
				failed: counts.TranslationFailed, failedTerminal: counts.TranslationFailedTerminal,
			}),
		Sentiment: enrichmentTypeStatus(
			switchedEnrichmentDisabledReason(s.sentimentConfigured,
				settings.Settings.SentimentEnrichmentEnabled()),
			enrichmentCounts{
				eligible: counts.SentimentEligible, done: counts.SentimentDone,
				failed: counts.SentimentFailed, failedTerminal: counts.SentimentFailedTerminal,
			}),
		Emotions: enrichmentTypeStatus(
			switchedEnrichmentDisabledReason(s.emotionsConfigured,
				settings.Settings.EmotionsEnrichmentEnabled()),
			enrichmentCounts{
				eligible: counts.EmotionsEligible, done: counts.EmotionsDone,
				failed: counts.EmotionsFailed, failedTerminal: counts.EmotionsFailedTerminal,
			}),
	}, nil
}

// switchedEnrichmentDisabledReason resolves the two gates guarding sentiment and emotions:
// the deployment provider+model gate, then the tenant's tri-state switch. An empty result means
// the enrichment is enabled.
//
// The deployment gate is reported first when both are closed. It is the outer gate and the one an
// operator can act on; telling a tenant "you switched it off" when no provider is configured at all
// would send them to a setting that changes nothing.
func switchedEnrichmentDisabledReason(configured, switchedOn bool) models.DisabledReason {
	switch {
	case !configured:
		return models.DisabledReasonNotConfigured
	case !switchedOn:
		return models.DisabledReasonSwitchedOff
	default:
		return ""
	}
}

// translationDisabledReason resolves translation's gates: the deployment provider+model gate, then
// a resolvable effective target language (the tenant's own, else the deployment default). An empty
// result means translation is enabled.
//
// Translation has no on/off switch, so it can never report switched_off — an unresolvable target
// IS its off state, and that is what no_target_language says.
func translationDisabledReason(configured bool, effectiveTarget string) models.DisabledReason {
	switch {
	case !configured:
		return models.DisabledReasonNotConfigured
	case effectiveTarget == "":
		return models.DisabledReasonNoTargetLanguage
	default:
		return ""
	}
}

// enrichmentTypeStatus assembles one enrichment's status from the gate decision, zeroing the counts
// when it is not enabled so the API never reports a backlog for work that will never run.
//
// Enabled and DisabledReason are derived from the same value here rather than passed separately, so
// the response cannot carry an enabled enrichment with a reason, or a disabled one without.
// enrichmentCounts groups one enrichment's four numbers so they travel together. Passing them as
// four positional int64s invited a silent transposition every time another pair was added.
type enrichmentCounts struct {
	eligible       int64
	done           int64
	failed         int64
	failedTerminal int64
}

func enrichmentTypeStatus(reason models.DisabledReason, counts enrichmentCounts) models.EnrichmentTypeStatus {
	if reason != "" {
		return models.EnrichmentTypeStatus{Enabled: false, DisabledReason: reason}
	}

	return models.EnrichmentTypeStatus{
		Enabled:        true,
		Eligible:       counts.eligible,
		Done:           counts.done,
		Failed:         counts.failed,
		FailedTerminal: counts.failedTerminal,
	}
}
