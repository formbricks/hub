package service

import (
	"context"
	"testing"

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
			settings:    &models.TenantSettings{Settings: models.EnrichmentSettings{TargetLanguage: "de-DE"}},
			wantTrans:   models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			wantSent:    models.EnrichmentTypeStatus{Enabled: false},
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
			wantTrans:   models.EnrichmentTypeStatus{Enabled: true, Eligible: 10, Done: 4},
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3},
			wantEmotion: models.EnrichmentTypeStatus{Enabled: false},
		},
		{
			name: "translation with no target and no default is zeroed",
			params: NewEnrichmentStatusServiceParams{
				DefaultLang: "", TranslationConfigured: true, SentimentConfigured: true, EmotionsConfigured: true,
			},
			settings:    &models.TenantSettings{Settings: models.EnrichmentSettings{}},
			wantTrans:   models.EnrichmentTypeStatus{Enabled: false},
			wantSent:    models.EnrichmentTypeStatus{Enabled: true, Eligible: 8, Done: 3},
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

			got, err := svc.GetEnrichmentStatus(context.Background(), "  tenant-1 ")
			require.NoError(t, err)

			assert.Equal(t, "tenant-1", got.TenantID, "tenant_id is normalized (trimmed)")
			assert.Equal(t, "tenant-1", repo.gotTenantID, "repo receives the normalized tenant_id")
			assert.Equal(t, testCase.wantTrans, got.Translation)
			assert.Equal(t, testCase.wantSent, got.Sentiment)
			assert.Equal(t, testCase.wantEmotion, got.Emotions)
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
