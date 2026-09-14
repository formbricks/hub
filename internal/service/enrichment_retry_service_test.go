package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type fakeRetryRepo struct {
	claimed   bool
	cleared   int64
	remaining time.Duration
	clearErr  error
	calls     int
}

func (f *fakeRetryRepo) ClearTerminalMarkers(
	_ context.Context, _, _ string, _ time.Duration,
) (bool, int64, error) {
	f.calls++

	return f.claimed, f.cleared, f.clearErr
}

func (f *fakeRetryRepo) CooldownRemaining(
	_ context.Context, _, _ string, _ time.Duration,
) (time.Duration, error) {
	return f.remaining, nil
}

type fakeSettingsReader struct{ settings models.TenantSettings }

func (f fakeSettingsReader) GetSettings(_ context.Context, tenantID string) (*models.TenantSettings, error) {
	out := f.settings
	out.TenantID = tenantID

	return &out, nil
}

func newRetryService(repo *fakeRetryRepo, reconcileEnabled bool) *EnrichmentRetryService {
	return NewEnrichmentRetryService(NewEnrichmentRetryServiceParams{
		Repo:                  repo,
		Settings:              fakeSettingsReader{},
		DefaultLang:           "en-US",
		TranslationConfigured: true,
		SentimentConfigured:   true,
		EmotionsConfigured:    true,
		ReconcileEnabled:      reconcileEnabled,
	})
}

// TestRetryRefusesWhenTheReconcilerIsOff pins the endpoint's contract to reality. Its 202 means
// "the next sweep picks these up" — with the kill switch off there is no sweep, so accepting would
// delete the markers, burn the cooldown, drop the failures from the status counts, and re-enqueue
// nothing. Coverage would silently look BETTER while nothing happened.
func TestRetryRefusesWhenTheReconcilerIsOff(t *testing.T) {
	repo := &fakeRetryRepo{claimed: true, cleared: 5}
	svc := newRetryService(repo, false)

	_, err := svc.Retry(context.Background(), "tenant-1", nil)
	require.Error(t, err)

	var conflict *huberrors.ConflictError
	require.ErrorAs(t, err, &conflict, "a refused retry is a loud conflict, not a quiet 202")
	assert.Zero(t, repo.calls, "and nothing was cleared or stamped")
}

// TestRetryReportsTheCeilingOfTheWait: reporting the floor invites a well-behaved caller to sleep
// exactly that long, retry a fraction of a second early, and be refused again.
func TestRetryReportsTheCeilingOfTheWait(t *testing.T) {
	repo := &fakeRetryRepo{claimed: false, remaining: 90*time.Second + 400*time.Millisecond}
	svc := newRetryService(repo, true)

	resp, err := svc.Retry(context.Background(), "tenant-1", []string{"sentiment"})
	require.NoError(t, err)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, models.RetryOutcomeCoolingDown, resp.Results[0].Outcome)
	assert.Equal(t, int64(91), resp.Results[0].RetryAfterSeconds,
		"90.4s remaining must report 91, not 90")
}

// TestRetryWindowDecisionBelongsToTheClear: the service must not pre-check the cooldown and then
// clear — the repository claims the window atomically with the delete, and the service's only job
// on refusal is to report the wait. One repo call on the success path is the proof.
func TestRetryWindowDecisionBelongsToTheClear(t *testing.T) {
	repo := &fakeRetryRepo{claimed: true, cleared: 3}
	svc := newRetryService(repo, true)

	resp, err := svc.Retry(context.Background(), "tenant-1", []string{"sentiment"})
	require.NoError(t, err)
	assert.Equal(t, models.RetryOutcomeCleared, resp.Results[0].Outcome)
	assert.Equal(t, int64(3), resp.Results[0].Cleared)
	assert.Equal(t, 1, repo.calls, "one atomic clear, no separate cooldown pre-read")
}
