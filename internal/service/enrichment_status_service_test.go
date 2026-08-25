package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

type fakeStatusRepo struct {
	counts         repository.EnrichmentStatusCounts
	err            error
	gotTenantID    string
	gotDefaultLang string
}

func (f *fakeStatusRepo) CountEnrichmentStatus(
	_ context.Context, tenantID, defaultLang string,
) (repository.EnrichmentStatusCounts, error) {
	f.gotTenantID = tenantID
	f.gotDefaultLang = defaultLang

	return f.counts, f.err
}

type fakeSettingsResolver struct {
	settings *models.TenantSettings
	err      error
}

func (f *fakeSettingsResolver) GetSettings(_ context.Context, tenantID string) (*models.TenantSettings, error) {
	if f.err != nil {
		return nil, f.err
	}

	if f.settings != nil {
		return f.settings, nil
	}

	return &models.TenantSettings{TenantID: tenantID}, nil
}

// fullCounts is a repo result with a distinct value in every bucket, so a test that expects a
// bucket to be zeroed proves the SERVICE zeroed it (not that the repo happened to return 0).
var fullCounts = repository.EnrichmentStatusCounts{
	TranslationEligible: 10, TranslationDone: 4,
	SentimentEligible: 8, SentimentDone: 3,
	EmotionsEligible: 6, EmotionsDone: 2,
	// Distinct values per bucket so a transposition between failed and failed_terminal, or across
	// enrichments, shows up as a wrong number rather than a coincidence.
	TranslationFailed: 5, TranslationFailedTerminal: 1,
	SentimentFailed: 4, SentimentFailedTerminal: 2,
	EmotionsFailed: 3, EmotionsFailedTerminal: 7,
}

func TestEnrichmentStatusService_GetEnrichmentStatus(t *testing.T) {
	emotionsOff := false
	sentimentOff := false

	cases := []struct {
		name        string
		params      NewEnrichmentStatusServiceParams
		settings    *models.TenantSettings
		wantTrans   models.EnrichmentTypeStatus
		wantSent    models.EnrichmentTypeStatus
		wantEmotion models.EnrichmentTypeStatus
	}{
		{
			name: "all configured and enabled",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings:    &models.TenantSettings{Settings: models.EnrichmentSettings{TargetLanguage: "de-DE"}},
			wantTrans:   models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4, Failed: 5, FailedTerminal: 1},
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3, Failed: 4, FailedTerminal: 2},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2, Failed: 3, FailedTerminal: 7},
		},
		{
			name: "sentiment not deployment-configured is zeroed",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: false, EmotionsConfigured: true,
			},
			settings:  &models.TenantSettings{Settings: models.EnrichmentSettings{TargetLanguage: "de-DE"}},
			wantTrans: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4, Failed: 5, FailedTerminal: 1},
			wantSent: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonNotConfigured,
			},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2, Failed: 3, FailedTerminal: 7},
		},
		{
			name: "tenant emotions switch off is zeroed",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings: &models.TenantSettings{Settings: models.EnrichmentSettings{
				TargetLanguage: "de-DE", EmotionsEnabled: &emotionsOff,
			}},
			wantTrans: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4, Failed: 5, FailedTerminal: 1},
			wantSent:  models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3, Failed: 4, FailedTerminal: 2},
			wantEmotion: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonSwitchedOff,
			},
		},
		{
			name: "translation with no target and no default is zeroed",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings: &models.TenantSettings{Settings: models.EnrichmentSettings{}},
			wantTrans: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonNoTargetLanguage,
			},
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3, Failed: 4, FailedTerminal: 2},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2, Failed: 3, FailedTerminal: 7},
		},
		{
			// Translation has a perfectly good target, so only the deployment gate can be the
			// reason — proves the target check does not shadow it.
			name: "translation not deployment-configured reports not_configured, not no_target_language",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: false, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings: &models.TenantSettings{Settings: models.EnrichmentSettings{TargetLanguage: "de-DE"}},
			wantTrans: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonNotConfigured,
			},
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3, Failed: 4, FailedTerminal: 2},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2, Failed: 3, FailedTerminal: 7},
		},
		{
			name: "tenant sentiment switch off is reported as switched_off",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings: &models.TenantSettings{Settings: models.EnrichmentSettings{
				TargetLanguage: "de-DE", SentimentEnabled: &sentimentOff,
			}},
			wantTrans: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4, Failed: 5, FailedTerminal: 1},
			wantSent: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonSwitchedOff,
			},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2, Failed: 3, FailedTerminal: 7},
		},
		{
			// Both gates closed at once. The deployment gate must win: sending a tenant to a switch
			// that changes nothing (because no provider is configured at all) is the wrong fix.
			name: "both gates closed reports the deployment gate, not the tenant switch",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: false, EmotionsConfigured: true,
			},
			settings: &models.TenantSettings{Settings: models.EnrichmentSettings{
				TargetLanguage: "de-DE", SentimentEnabled: &sentimentOff,
			}},
			wantTrans: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4, Failed: 5, FailedTerminal: 1},
			wantSent: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonNotConfigured,
			},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2, Failed: 3, FailedTerminal: 7},
		},
		{
			name: "translation enabled via default-language fallback",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings:    &models.TenantSettings{Settings: models.EnrichmentSettings{}},
			wantTrans:   models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4, Failed: 5, FailedTerminal: 1},
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3, Failed: 4, FailedTerminal: 2},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2, Failed: 3, FailedTerminal: 7},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &fakeStatusRepo{counts: fullCounts}
			params := testCase.params
			params.Repo = repo
			params.Settings = &fakeSettingsResolver{settings: testCase.settings}

			svc := NewEnrichmentStatusService(params)

			got, err := svc.GetEnrichmentStatus(context.Background(), "  tenant-1 ")
			require.NoError(t, err)

			assert.Equal(t, "tenant-1", got.TenantID, "tenant_id is normalized (trimmed)")
			assert.Equal(t, "tenant-1", repo.gotTenantID, "repo receives the normalized tenant_id")
			assert.Equal(t, testCase.wantTrans, got.Translation)
			assert.Equal(t, testCase.wantSent, got.Sentiment)
			assert.Equal(t, testCase.wantEmotion, got.Emotions)

			// AsOf must be a real stamp, not the zero value the JSON encoder would happily render as
			// year 1 — a client differencing two of those gets nonsense.
			//
			// Bounded generously rather than pinned between two time.Now() reads taken around the
			// call: .UTC() strips the monotonic clock reading, so tight bounds are pure wall-clock
			// and an NTP step backwards mid-test would fail them with no defect in the code. An hour
			// still catches a zero value or a wrong clock source, and cannot flake.
			assert.False(t, got.AsOf.IsZero(), "as_of is stamped")
			assert.WithinDuration(t, time.Now().UTC(), got.AsOf, time.Hour, "as_of is a current stamp")
			assert.Equal(t, time.UTC, got.AsOf.Location(), "as_of is UTC")

			// A disabled enrichment reports no counts at all, including failures: the API must not
			// show a backlog of failed work for a pipeline that will never run.
			for name, status := range map[string]models.EnrichmentTypeStatus{
				"translation": got.Translation, "sentiment": got.Sentiment, "emotions": got.Emotions,
			} {
				if !status.Enabled {
					assert.Zero(t, status.Failed, "%s: disabled must report no failures", name)
					assert.Zero(t, status.FailedTerminal, "%s: disabled must report no terminal failures", name)
				}
			}

			// enabled and disabled_reason are derived from one decision, so they can never disagree.
			for name, status := range map[string]models.EnrichmentTypeStatus{
				"translation": got.Translation, "sentiment": got.Sentiment, "emotions": got.Emotions,
			} {
				if status.Enabled {
					assert.Empty(t, status.DisabledReason, "%s: enabled must carry no reason", name)
				} else {
					assert.NotEmpty(t, status.DisabledReason, "%s: disabled must carry a reason", name)
				}
			}
		})
	}
}

func TestEnrichmentStatusService_RequiresTenantID(t *testing.T) {
	svc := NewEnrichmentStatusService(NewEnrichmentStatusServiceParams{
		Repo:     &fakeStatusRepo{},
		Settings: &fakeSettingsResolver{},
	})

	_, err := svc.GetEnrichmentStatus(context.Background(), "   ")
	require.Error(t, err)

	// normalizeRequiredTenantIDValue returns a *huberrors.ValidationError, which the response
	// layer maps to a client 400 rather than a generic 500.
	var validationErr *huberrors.ValidationError
	assert.ErrorAs(t, err, &validationErr, "missing tenant_id must be a validation error")
}
