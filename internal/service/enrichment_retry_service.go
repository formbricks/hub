package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// enrichmentRetryQueryTimeout bounds the whole request. The work is a handful of keyed deletes and
// upserts, so this is a ceiling rather than a budget.
const enrichmentRetryQueryTimeout = 10 * time.Second

// EnrichmentRetryCooldown is how long a tenant must wait between clears of the same enrichment.
//
// The number is a judgement, not a measurement: long enough that clear-and-sweep cannot be run in
// a loop, short enough that a genuine "the provider changed its policy, try again" is not an
// overnight wait. Bounds the amplification to (size of terminal set / this).
const EnrichmentRetryCooldown = time.Hour

// EnrichmentRetryRepository clears terminal markers and tracks the cooldown.
type EnrichmentRetryRepository interface {
	ClearTerminalMarkers(ctx context.Context, tenantID, enrichment string) (int64, error)
	CooldownRemaining(ctx context.Context, tenantID, enrichment string, window time.Duration) (time.Duration, error)
}

// EnrichmentRetryService gives permanently-failed records another chance.
//
// The automatic sweep will not: a terminal failure is a property of the record's own text, so
// re-running it costs a provider call and fails identically. This is the deliberate override for
// when the premise has changed — the text was edited, or the provider changed its policy.
type EnrichmentRetryService struct {
	repo     EnrichmentRetryRepository
	settings TenantSettingsReader
	gates    enrichmentGates
	cooldown time.Duration
}

// enrichmentGates is the deployment half of "is this enrichment running", shared with the status
// service so the two cannot disagree about what is enabled.
type enrichmentGates struct {
	defaultLang           string
	translationConfigured bool
	sentimentConfigured   bool
	emotionsConfigured    bool
}

// NewEnrichmentRetryServiceParams configures the retry service.
type NewEnrichmentRetryServiceParams struct {
	Repo                  EnrichmentRetryRepository
	Settings              TenantSettingsReader
	DefaultLang           string
	TranslationConfigured bool
	SentimentConfigured   bool
	EmotionsConfigured    bool
	// Cooldown overrides the default window. Zero uses EnrichmentRetryCooldown; tests set it short.
	Cooldown time.Duration
}

// NewEnrichmentRetryService creates an enrichment retry service.
func NewEnrichmentRetryService(params NewEnrichmentRetryServiceParams) *EnrichmentRetryService {
	cooldown := params.Cooldown
	if cooldown <= 0 {
		cooldown = EnrichmentRetryCooldown
	}

	return &EnrichmentRetryService{
		repo:     params.Repo,
		settings: params.Settings,
		cooldown: cooldown,
		gates: enrichmentGates{
			defaultLang:           params.DefaultLang,
			translationConfigured: params.TranslationConfigured,
			sentimentConfigured:   params.SentimentConfigured,
			emotionsConfigured:    params.EmotionsConfigured,
		},
	}
}

// Retry clears terminal failure markers for the named enrichments, or for all three when none are
// named, so the next reconcile sweep picks those records up again.
//
// Every enrichment gets an outcome, including the ones that were refused. A caller that cannot
// tell "there was nothing to clear" from "you are being rate limited" will simply call again,
// which is the behaviour the cooldown exists to stop.
func (s *EnrichmentRetryService) Retry(
	ctx context.Context, tenantID string, enrichments []string,
) (*models.EnrichmentRetryResponse, error) {
	normalizedTenantID, err := normalizeRequiredTenantIDValue(tenantID)
	if err != nil {
		return nil, err
	}

	requested, err := normalizeRequestedEnrichments(enrichments)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, enrichmentRetryQueryTimeout)
	defer cancel()

	settings, err := s.settings.GetSettings(ctx, normalizedTenantID)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant settings: %w", err)
	}

	response := &models.EnrichmentRetryResponse{
		TenantID: normalizedTenantID,
		Results:  make([]models.EnrichmentRetryResult, 0, len(requested)),
	}

	for _, enrichment := range requested {
		result, retryErr := s.retryOne(ctx, normalizedTenantID, enrichment, settings)
		if retryErr != nil {
			return nil, retryErr
		}

		response.Results = append(response.Results, result)
	}

	return response, nil
}

// retryOne resolves one enrichment: refuse if it is not running, refuse if the tenant cleared it
// too recently, otherwise clear.
func (s *EnrichmentRetryService) retryOne(
	ctx context.Context, tenantID, enrichment string, settings *models.TenantSettings,
) (models.EnrichmentRetryResult, error) {
	result := models.EnrichmentRetryResult{Enrichment: enrichment}

	// Refuse before spending anything when the enrichment will not run: clearing markers for a
	// switched-off pipeline queues work the worker's own gate skips, so the records would be
	// re-marked and the caller's cooldown burned for nothing.
	if reason := s.disabledReason(enrichment, settings); reason != "" {
		result.Outcome = models.RetryOutcomeDisabled
		result.DisabledReason = reason

		return result, nil
	}

	remaining, err := s.repo.CooldownRemaining(ctx, tenantID, enrichment, s.cooldown)
	if err != nil {
		return result, fmt.Errorf("read retry cooldown: %w", err)
	}

	if remaining > 0 {
		result.Outcome = models.RetryOutcomeCoolingDown
		// Rounded UP: reporting the floor invites a caller to retry a fraction of a second early
		// and be refused again.
		result.RetryAfterSeconds = int64(remaining.Round(time.Second).Seconds())
		if result.RetryAfterSeconds == 0 {
			result.RetryAfterSeconds = 1
		}

		return result, nil
	}

	cleared, err := s.repo.ClearTerminalMarkers(ctx, tenantID, enrichment)
	if err != nil {
		return result, fmt.Errorf("clear terminal failures: %w", err)
	}

	result.Outcome = models.RetryOutcomeCleared
	result.Cleared = cleared

	// The Hub has no audit logging, and this is a caller-triggered action that spends provider
	// money on records already known to fail. This line is what answers "who caused this bill".
	slog.InfoContext(ctx, "enrichment retry: terminal failures cleared",
		"tenant_id", tenantID, "enrichment", enrichment, "cleared", cleared)

	return result, nil
}

// disabledReason reports which gate is closed for this enrichment, or "" when it is running. The
// values match the status endpoint's, so a consumer needs one vocabulary rather than two.
func (s *EnrichmentRetryService) disabledReason(
	enrichment string, settings *models.TenantSettings,
) models.DisabledReason {
	switch enrichment {
	case models.EnrichmentNameSentiment:
		return switchedEnrichmentDisabledReason(
			s.gates.sentimentConfigured, settings.Settings.SentimentEnrichmentEnabled())
	case models.EnrichmentNameEmotions:
		return switchedEnrichmentDisabledReason(
			s.gates.emotionsConfigured, settings.Settings.EmotionsEnrichmentEnabled())
	case models.EnrichmentNameTranslation:
		return translationDisabledReason(s.gates.translationConfigured,
			resolveTargetLang(settings.Settings.TargetLanguage, s.gates.defaultLang))
	default:
		return ""
	}
}

// normalizeRequestedEnrichments validates the requested set, defaulting to all three.
//
// An unknown name is rejected rather than ignored. Silently dropping it would answer a request to
// clear "sentimnet" with a cheerful 202 and an empty result list, and the caller would conclude
// their retry had nothing to do.
func normalizeRequestedEnrichments(requested []string) ([]string, error) {
	all := []string{
		models.EnrichmentNameTranslation,
		models.EnrichmentNameSentiment,
		models.EnrichmentNameEmotions,
	}

	if len(requested) == 0 {
		return all, nil
	}

	known := make(map[string]struct{}, len(all))
	for _, name := range all {
		known[name] = struct{}{}
	}

	seen := make(map[string]struct{}, len(requested))
	out := make([]string, 0, len(requested))

	for _, name := range requested {
		trimmed := strings.TrimSpace(name)
		if _, ok := known[trimmed]; !ok {
			return nil, huberrors.NewValidationError("enrichments",
				fmt.Sprintf("unknown enrichment %q; expected one of translation, sentiment, emotions", trimmed))
		}

		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}

		seen[trimmed] = struct{}{}

		out = append(out, trimmed)
	}

	return out, nil
}
