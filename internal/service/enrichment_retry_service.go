package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
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

// EnrichmentRetryRepository clears terminal markers and tracks the cooldown. ClearTerminalMarkers
// makes the window decision itself, atomically with the delete; CooldownRemaining only reports the
// wait for a refused caller's response.
type EnrichmentRetryRepository interface {
	ClearTerminalMarkers(ctx context.Context, tenantID, enrichment string, window time.Duration) (claimed bool, cleared int64, err error)
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
	// reconcileEnabled mirrors ENRICHMENT_RECONCILE_ENABLED. The endpoint's whole contract is
	// "cleared markers are picked up by the next sweep" — with the sweep switched off, honoring a
	// clear would delete the markers, burn the cooldown, drop the failures from the status counts,
	// and re-enqueue nothing: coverage would silently look BETTER while nothing happened.
	reconcileEnabled bool
	// metrics may be nil (metrics disabled); every use is guarded.
	metrics observability.EnrichmentReconcileMetrics
}

// enrichmentGates is the deployment half of "is this enrichment running", and — via
// disabledReasonFor — the ONE mapping from an enrichment name to its pair of gates. Both the
// status service and this one resolve through it, so the two endpoints cannot disagree about
// whether an enrichment is enabled: the previous shape kept the leaf helpers shared but duplicated
// the name→gates pairing in two switches, which is exactly the kind of split that drifts when a
// fourth enrichment arrives.
type enrichmentGates struct {
	defaultLang           string
	translationConfigured bool
	sentimentConfigured   bool
	emotionsConfigured    bool
}

// disabledReasonFor reports which gate is closed for the named enrichment given a tenant's
// settings, or "" when it is running.
func (g enrichmentGates) disabledReasonFor(
	enrichment string, settings *models.TenantSettings,
) models.DisabledReason {
	switch enrichment {
	case models.EnrichmentNameSentiment:
		return switchedEnrichmentDisabledReason(
			g.sentimentConfigured, settings.Settings.SentimentEnrichmentEnabled())
	case models.EnrichmentNameEmotions:
		return switchedEnrichmentDisabledReason(
			g.emotionsConfigured, settings.Settings.EmotionsEnrichmentEnabled())
	case models.EnrichmentNameTranslation:
		return translationDisabledReason(g.translationConfigured,
			resolveTargetLang(settings.Settings.TargetLanguage, g.defaultLang))
	default:
		return ""
	}
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
	// ReconcileEnabled is cfg.EnrichmentReconcile.Enabled — whether anything will ever act on a
	// clear. False makes Retry refuse outright rather than accept a no-op.
	ReconcileEnabled bool
	// Metrics counts each enrichment's retry outcome. nil disables them; retries still work.
	Metrics observability.EnrichmentReconcileMetrics
}

// NewEnrichmentRetryService creates an enrichment retry service.
func NewEnrichmentRetryService(params NewEnrichmentRetryServiceParams) *EnrichmentRetryService {
	cooldown := params.Cooldown
	if cooldown <= 0 {
		cooldown = EnrichmentRetryCooldown
	}

	return &EnrichmentRetryService{
		repo:             params.Repo,
		settings:         params.Settings,
		cooldown:         cooldown,
		reconcileEnabled: params.ReconcileEnabled,
		metrics:          params.Metrics,
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
	// Refused BEFORE validation spends anything: with the reconciler off there is no sweep to pick
	// the cleared records up, so a 202 here would be a lie the caller pays a cooldown for.
	if !s.reconcileEnabled {
		return nil, huberrors.NewConflictError(
			"the enrichment reconciler is disabled on this deployment (ENRICHMENT_RECONCILE_ENABLED); " +
				"clearing failures would have no effect until it is re-enabled")
	}

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
			// Each clear that already succeeded deleted markers and burned a cooldown, and the
			// caller is about to see only an error. Without this line the next call answers
			// `cooling_down` for those enrichments and nobody can explain why -- the work
			// happened, the response that would have said so was discarded. The results are
			// not returned alongside the error because a partial success under a failure status
			// is worse to consume than a log line is to read.
			slog.ErrorContext(ctx, "enrichment retry: failed part-way through a multi-enrichment request",
				"tenant_id", normalizedTenantID, "failed_on", enrichment,
				"already_cleared", response.Results, "error", retryErr)

			return nil, retryErr
		}

		if s.metrics != nil {
			s.metrics.RecordRetry(ctx, enrichment, string(result.Outcome))
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
	if reason := s.gates.disabledReasonFor(enrichment, settings); reason != "" {
		result.Outcome = models.RetryOutcomeDisabled
		result.DisabledReason = reason

		return result, nil
	}

	// The repository decides the window and clears in one atomic statement — a read-then-clear
	// here would let two concurrent requests both pass the check, and the cooldown is the one
	// bound this endpoint has.
	claimed, cleared, err := s.repo.ClearTerminalMarkers(ctx, tenantID, enrichment, s.cooldown)
	if err != nil {
		return result, fmt.Errorf("clear terminal failures: %w", err)
	}

	if !claimed {
		remaining, waitErr := s.repo.CooldownRemaining(ctx, tenantID, enrichment, s.cooldown)
		if waitErr != nil {
			return result, fmt.Errorf("read retry cooldown: %w", waitErr)
		}

		result.Outcome = models.RetryOutcomeCoolingDown
		// CEILED, not rounded: reporting the floor invites a well-behaved caller to sleep exactly
		// that long, retry a fraction of a second early, and be refused again.
		result.RetryAfterSeconds = max(int64(math.Ceil(remaining.Seconds())), 1)

		return result, nil
	}

	result.Outcome = models.RetryOutcomeCleared
	result.Cleared = cleared

	// The Hub has no audit logging, and this is a caller-triggered action that spends provider
	// money on records already known to fail. This line is what answers "who caused this bill".
	slog.InfoContext(ctx, "enrichment retry: terminal failures cleared",
		"tenant_id", tenantID, "enrichment", enrichment, "cleared", cleared)

	return result, nil
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
