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
			wantTrans:   models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2},
		},
		{
			name: "sentiment not deployment-configured is zeroed",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: false, EmotionsConfigured: true,
			},
			settings:  &models.TenantSettings{Settings: models.EnrichmentSettings{TargetLanguage: "de-DE"}},
			wantTrans: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			wantSent: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonNotConfigured,
			},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2},
		},
		{
			name: "tenant emotions switch off is zeroed",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings: &models.TenantSettings{Settings: models.EnrichmentSettings{
				TargetLanguage: "de-DE", EmotionsEnabled: &emotionsOff,
			}},
			wantTrans: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			wantSent:  models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3},
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
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2},
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
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2},
		},
		{
			name: "tenant sentiment switch off is reported as switched_off",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings: &models.TenantSettings{Settings: models.EnrichmentSettings{
				TargetLanguage: "de-DE", SentimentEnabled: &sentimentOff,
			}},
			wantTrans: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			wantSent: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonSwitchedOff,
			},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2},
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
			wantTrans: models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			wantSent: models.EnrichmentTypeStatus{
				Enabled: false, DisabledReason: models.DisabledReasonNotConfigured,
			},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2},
		},
		{
			name: "translation enabled via default-language fallback",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "en-US", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings:    &models.TenantSettings{Settings: models.EnrichmentSettings{}},
			wantTrans:   models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: true, Eligible: 6, Done: 2},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &fakeStatusRepo{counts: fullCounts}
			params := testCase.params
			params.Repo = repo
			params.Settings = &fakeSettingsResolver{settings: testCase.settings}

			svc := NewEnrichmentStatusService(params)

			before := time.Now().UTC()
			got, err := svc.GetEnrichmentStatus(context.Background(), "  tenant-1 ")
			require.NoError(t, err)

			assert.Equal(t, "tenant-1", got.TenantID, "tenant_id is normalized (trimmed)")
			assert.Equal(t, "tenant-1", repo.gotTenantID, "repo receives the normalized tenant_id")
			assert.Equal(t, testCase.wantTrans, got.Translation)
			assert.Equal(t, testCase.wantSent, got.Sentiment)
			assert.Equal(t, testCase.wantEmotion, got.Emotions)

			// AsOf must be a real stamp taken during the call, not a zero value the JSON encoder
			// would happily render as year 1 — a client differencing two of those gets nonsense.
			assert.False(t, got.AsOf.IsZero(), "as_of is stamped")
			assert.False(t, got.AsOf.Before(before), "as_of is taken during the call")
			assert.False(t, got.AsOf.After(time.Now().UTC()), "as_of is not in the future")
			assert.Equal(t, time.UTC, got.AsOf.Location(), "as_of is UTC")

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
