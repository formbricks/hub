package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockFeedbackRecordsRepo struct {
	record                     *models.FeedbackRecord
	createReq                  *models.CreateFeedbackRecordRequest
	deleteByUserGroups         []models.DeletedFeedbackRecordsByTenant
	deletedID                  uuid.UUID
	deleteByUserFilters        *models.DeleteFeedbackRecordsByUserFilters
	translationBackfillTargets []models.TranslationBackfillTarget
	translationBackfillErr     error
	tenantBackfillTargets      []models.TranslationBackfillTarget
	tenantBackfillErr          error
	sentimentBackfillTargets   []uuid.UUID
	sentimentBackfillErr       error
	emotionsBackfillTargets    []uuid.UUID
	emotionsBackfillErr        error

	setSentimentCalled bool
	setSentimentLabel  *models.SentimentValue
	setSentimentScore  *float64

	setEmotionsCalled bool
	setEmotionsLabels []models.EmotionValue
}

func (m *mockFeedbackRecordsRepo) Create(
	_ context.Context, req *models.CreateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	reqCopy := *req
	m.createReq = &reqCopy

	if m.record != nil {
		return m.record, nil
	}

	return &models.FeedbackRecord{TenantID: req.TenantID}, nil
}

func (m *mockFeedbackRecordsRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.FeedbackRecord, error) {
	return m.record, nil
}

func (m *mockFeedbackRecordsRepo) List(
	_ context.Context, _ *models.ListFeedbackRecordsFilters,
) ([]models.FeedbackRecord, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (m *mockFeedbackRecordsRepo) ListAfterCursor(
	_ context.Context, _ *models.ListFeedbackRecordsFilters, _ time.Time, _ uuid.UUID,
) ([]models.FeedbackRecord, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (m *mockFeedbackRecordsRepo) Update(
	_ context.Context, _ uuid.UUID, _ *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	if m.record != nil {
		return m.record, nil
	}

	return nil, errors.New("not implemented")
}

func (m *mockFeedbackRecordsRepo) SetTranslation(
	_ context.Context, _ uuid.UUID, _ *string, _, _ string,
) error {
	return nil
}

func (m *mockFeedbackRecordsRepo) SetSentiment(
	_ context.Context, _ uuid.UUID, sentiment *models.SentimentValue, score *float64,
) error {
	m.setSentimentCalled = true
	m.setSentimentLabel = sentiment
	m.setSentimentScore = score

	return nil
}

func (m *mockFeedbackRecordsRepo) SetEmotions(
	_ context.Context, _ uuid.UUID, emotions []models.EmotionValue,
) error {
	m.setEmotionsCalled = true
	m.setEmotionsLabels = emotions

	return nil
}

func (m *mockFeedbackRecordsRepo) ListTranslationBackfillTargets(
	_ context.Context, afterID uuid.UUID, _ int, _ string,
) ([]models.TranslationBackfillTarget, error) {
	if m.translationBackfillErr != nil {
		return nil, m.translationBackfillErr
	}

	// First page (afterID == Nil) returns the configured set; later pages are empty.
	if afterID != uuid.Nil {
		return nil, nil
	}

	return m.translationBackfillTargets, nil
}

func (m *mockFeedbackRecordsRepo) ListTranslationBackfillTargetsForTenant(
	_ context.Context, _ string, afterID uuid.UUID, _ int, _ string,
) ([]models.TranslationBackfillTarget, error) {
	if m.tenantBackfillErr != nil {
		return nil, m.tenantBackfillErr
	}

	// First page (afterID == Nil) returns the configured set; later pages are empty, so
	// the keyset loop terminates after one short page.
	if afterID != uuid.Nil {
		return nil, nil
	}

	return m.tenantBackfillTargets, nil
}

func (m *mockFeedbackRecordsRepo) ListSentimentBackfillTargets(
	_ context.Context, afterID uuid.UUID, _ int,
) ([]uuid.UUID, error) {
	if m.sentimentBackfillErr != nil {
		return nil, m.sentimentBackfillErr
	}

	if afterID != uuid.Nil {
		return nil, nil
	}

	return m.sentimentBackfillTargets, nil
}

func (m *mockFeedbackRecordsRepo) ListEmotionsBackfillTargets(
	_ context.Context, afterID uuid.UUID, _ int,
) ([]uuid.UUID, error) {
	if m.emotionsBackfillErr != nil {
		return nil, m.emotionsBackfillErr
	}

	if afterID != uuid.Nil {
		return nil, nil
	}

	return m.emotionsBackfillTargets, nil
}

func (m *mockFeedbackRecordsRepo) Delete(_ context.Context, id uuid.UUID) error {
	m.deletedID = id

	return nil
}

func (m *mockFeedbackRecordsRepo) DeleteByUser(
	_ context.Context, filters *models.DeleteFeedbackRecordsByUserFilters,
) ([]models.DeletedFeedbackRecordsByTenant, error) {
	m.deleteByUserFilters = filters

	return m.deleteByUserGroups, nil
}

func TestFeedbackRecordsService_DeleteFeedbackRecord_PublishesTenantAwareDeletedEvent(t *testing.T) {
	ctx := context.Background()
	recordID := uuid.Must(uuid.NewV7())
	tenantID := "org-123"
	repo := &mockFeedbackRecordsRepo{record: &models.FeedbackRecord{ID: recordID, TenantID: tenantID}}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	err := svc.DeleteFeedbackRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("DeleteFeedbackRecord() error = %v", err)
	}

	if repo.deletedID != recordID {
		t.Fatalf("deletedID = %v, want %v", repo.deletedID, recordID)
	}

	assertDeletedEventData(t, publisher, datatypes.FeedbackRecordDeleted, tenantID, []uuid.UUID{recordID})
}

func TestFeedbackRecordsService_CreateFeedbackRecord_NormalizesTenantID(t *testing.T) {
	ctx := context.Background()
	inputTenantID := " org-123 "
	repo := &mockFeedbackRecordsRepo{}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	record, err := svc.CreateFeedbackRecord(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "field-1",
		FieldType:    models.FieldTypeText,
		TenantID:     inputTenantID,
		SubmissionID: "submission-1",
	})
	if err != nil {
		t.Fatalf("CreateFeedbackRecord() error = %v", err)
	}

	if repo.createReq == nil {
		t.Fatal("repo Create request = nil")
	}

	if repo.createReq.TenantID != "org-123" {
		t.Fatalf("repo TenantID = %q, want org-123", repo.createReq.TenantID)
	}

	if record.TenantID != "org-123" {
		t.Fatalf("record TenantID = %q, want org-123", record.TenantID)
	}

	if publisher.callCount != 1 || publisher.eventType != datatypes.FeedbackRecordCreated {
		t.Fatalf("published event = (%d, %s), want one feedback_record.created", publisher.callCount, publisher.eventType)
	}
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_PublishesTenantAwareDeletedEventsByTenant(t *testing.T) {
	ctx := context.Background()
	tenantA := "org-123"
	tenantB := "org-456"
	tenantAIDs := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	tenantBIDs := []uuid.UUID{uuid.Must(uuid.NewV7())}
	repo := &mockFeedbackRecordsRepo{
		deleteByUserGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: tenantA, IDs: tenantAIDs},
			{TenantID: tenantB, IDs: tenantBIDs},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{UserID: " user-123 "})
	if err != nil {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v", err)
	}

	if repo.deleteByUserFilters == nil {
		t.Fatal("repo DeleteByUser filters = nil")
	}

	if repo.deleteByUserFilters.UserID != "user-123" {
		t.Fatalf("repo UserID = %q, want user-123", repo.deleteByUserFilters.UserID)
	}

	if repo.deleteByUserFilters.TenantID != nil {
		t.Fatalf("repo TenantID = %q, want nil for all-tenant delete", *repo.deleteByUserFilters.TenantID)
	}

	if count != len(tenantAIDs)+len(tenantBIDs) {
		t.Fatalf("count = %d, want %d", count, len(tenantAIDs)+len(tenantBIDs))
	}

	assertDeletedEventDataAt(t, publisher, 0, datatypes.FeedbackRecordDeleted, tenantA, tenantAIDs)
	assertDeletedEventDataAt(t, publisher, 1, datatypes.FeedbackRecordDeleted, tenantB, tenantBIDs)
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_NormalizesTenantFilter(t *testing.T) {
	ctx := context.Background()
	tenantID := " org-123 "
	deletedID := uuid.Must(uuid.NewV7())
	repo := &mockFeedbackRecordsRepo{
		deleteByUserGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: "org-123", IDs: []uuid.UUID{deletedID}},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{
		UserID:   "user-123",
		TenantID: &tenantID,
	})
	if err != nil {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v", err)
	}

	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	if repo.deleteByUserFilters == nil || repo.deleteByUserFilters.TenantID == nil {
		t.Fatal("repo TenantID = nil, want normalized tenant")
	}

	if *repo.deleteByUserFilters.TenantID != "org-123" {
		t.Fatalf("repo TenantID = %q, want org-123", *repo.deleteByUserFilters.TenantID)
	}

	assertDeletedEventData(t, publisher, datatypes.FeedbackRecordDeleted, "org-123", []uuid.UUID{deletedID})
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_RejectsOverlengthTenantFilter(t *testing.T) {
	ctx := context.Background()
	tenantID := strings.Repeat("a", maxTenantIDLength+1)
	repo := &mockFeedbackRecordsRepo{}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{
		UserID:   "user-123",
		TenantID: &tenantID,
	})
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v, want validation error", err)
	}

	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	if repo.deleteByUserFilters != nil {
		t.Fatal("repo DeleteByUser was called, want validation before repository")
	}

	if publisher.callCount != 0 {
		t.Fatalf("published %d events, want 0", publisher.callCount)
	}
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_RejectsOverlengthUserID(t *testing.T) {
	ctx := context.Background()
	userID := strings.Repeat("a", maxUserIDLength+1)
	repo := &mockFeedbackRecordsRepo{}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{UserID: userID})
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v, want validation error", err)
	}

	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	if repo.deleteByUserFilters != nil {
		t.Fatal("repo DeleteByUser was called, want validation before repository")
	}

	if publisher.callCount != 0 {
		t.Fatalf("published %d events, want 0", publisher.callCount)
	}
}

func TestFeedbackRecordsService_DeleteFeedbackRecordsByUser_RequiresUserID(t *testing.T) {
	ctx := context.Background()
	repo := &mockFeedbackRecordsRepo{
		deleteByUserGroups: []models.DeletedFeedbackRecordsByTenant{
			{TenantID: "org-123", IDs: []uuid.UUID{uuid.Must(uuid.NewV7())}},
		},
	}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	count, err := svc.DeleteFeedbackRecordsByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{})
	if !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("DeleteFeedbackRecordsByUser() error = %v, want ErrUserIDRequired", err)
	}

	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	if publisher.callCount != 0 {
		t.Fatalf("published %d events, want 0", publisher.callCount)
	}
}

func TestFeedbackRecordsService_SetTranslation_RequiresLangKey(t *testing.T) {
	svc := NewFeedbackRecordsService(&mockFeedbackRecordsRepo{}, nil, "", nil, nil, "", 0, "")
	translated := "Hallo"

	// A translation with a blank lang key is rejected before reaching the repo.
	if err := svc.SetTranslation(context.Background(), uuid.New(), &translated, "  "); !errors.Is(err, ErrTranslationLangKeyRequired) {
		t.Fatalf("err = %v, want ErrTranslationLangKeyRequired", err)
	}

	// Clearing (nil translation) with an empty key is allowed.
	if err := svc.SetTranslation(context.Background(), uuid.New(), nil, ""); err != nil {
		t.Fatalf("clear err = %v, want nil", err)
	}
}

func TestFeedbackRecordsService_SetSentiment_Persists(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	label := models.SentimentPositive
	score := 0.5

	if err := svc.SetSentiment(context.Background(), uuid.New(), &label, &score); err != nil {
		t.Fatalf("SetSentiment() error = %v", err)
	}

	if !repo.setSentimentCalled || repo.setSentimentLabel == nil || repo.setSentimentScore == nil {
		t.Fatalf("repo not called with label+score: %+v", repo)
	}

	if *repo.setSentimentLabel != models.SentimentPositive || *repo.setSentimentScore != 0.5 {
		t.Fatalf("repo got (%v, %v), want (positive, 0.5)", *repo.setSentimentLabel, *repo.setSentimentScore)
	}
}

func TestFeedbackRecordsService_SetSentiment_ClearsWithNil(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	if err := svc.SetSentiment(context.Background(), uuid.New(), nil, nil); err != nil {
		t.Fatalf("SetSentiment(clear) error = %v", err)
	}

	if !repo.setSentimentCalled {
		t.Fatal("clearing must still write (nulls both columns)")
	}

	if repo.setSentimentLabel != nil || repo.setSentimentScore != nil {
		t.Fatalf("clear passed (%v, %v), want (nil, nil)", repo.setSentimentLabel, repo.setSentimentScore)
	}
}

func TestFeedbackRecordsService_SetSentiment_RejectsInvalidLabelAndMissingScore(t *testing.T) {
	invalid := models.SentimentValue("furious")
	valid := models.SentimentNeutral
	score := 0.0

	t.Run("invalid label is rejected before the repo", func(t *testing.T) {
		repo := &mockFeedbackRecordsRepo{}
		svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

		if err := svc.SetSentiment(context.Background(), uuid.New(), &invalid, &score); !errors.Is(err, ErrInvalidSentimentLabel) {
			t.Fatalf("err = %v, want ErrInvalidSentimentLabel", err)
		}

		if repo.setSentimentCalled {
			t.Fatal("an invalid label must not reach the repo")
		}
	})

	t.Run("a label without a score is rejected", func(t *testing.T) {
		repo := &mockFeedbackRecordsRepo{}
		svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

		if err := svc.SetSentiment(context.Background(), uuid.New(), &valid, nil); !errors.Is(err, ErrSentimentScoreRequired) {
			t.Fatalf("err = %v, want ErrSentimentScoreRequired", err)
		}

		if repo.setSentimentCalled {
			t.Fatal("a label without a score must not reach the repo")
		}
	})
}

func TestFeedbackRecordsService_SetEmotions_Persists(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	emotions := []models.EmotionValue{models.EmotionJoy, models.EmotionSurprise}

	if err := svc.SetEmotions(context.Background(), uuid.New(), emotions); err != nil {
		t.Fatalf("SetEmotions() error = %v", err)
	}

	if got := repo.setEmotionsLabels; !repo.setEmotionsCalled || len(got) != 2 ||
		got[0] != models.EmotionJoy || got[1] != models.EmotionSurprise {
		t.Fatalf("repo got %v (called=%v), want [joy surprise]", got, repo.setEmotionsCalled)
	}
}

func TestFeedbackRecordsService_SetEmotions_ClearsWithEmpty(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	// An empty set (the "no emotion detected" worker path) clears rather than writing {}.
	if err := svc.SetEmotions(context.Background(), uuid.New(), []models.EmotionValue{}); err != nil {
		t.Fatalf("SetEmotions(clear) error = %v", err)
	}

	if !repo.setEmotionsCalled {
		t.Fatal("clearing must still write (nulls the column)")
	}

	if repo.setEmotionsLabels != nil {
		t.Fatalf("clear passed %v, want nil (an empty set clears)", repo.setEmotionsLabels)
	}
}

func TestFeedbackRecordsService_SetEmotions_RejectsInvalidLabel(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	emotions := []models.EmotionValue{models.EmotionJoy, models.EmotionValue("ecstatic")}

	if err := svc.SetEmotions(context.Background(), uuid.New(), emotions); !errors.Is(err, ErrInvalidEmotionLabel) {
		t.Fatalf("err = %v, want ErrInvalidEmotionLabel", err)
	}

	if repo.setEmotionsCalled {
		t.Fatal("an invalid label must not reach the repo")
	}
}

func TestFeedbackRecordsService_BackfillTranslationsForTenant(t *testing.T) {
	firstID := uuid.New()
	secondID := uuid.New()
	repo := &mockFeedbackRecordsRepo{
		tenantBackfillTargets: []models.TranslationBackfillTarget{
			{FeedbackRecordID: firstID, TargetLang: "de-DE"},
			{FeedbackRecordID: secondID, TargetLang: "de-DE"},
		},
	}
	inserter := &mockTranslationInserter{}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	enqueued, err := svc.BackfillTranslationsForTenant(context.Background(), inserter, TranslationsQueueName, 3, "org-1", "run-1")
	if err != nil {
		t.Fatalf("BackfillTranslationsForTenant() error = %v", err)
	}

	if enqueued != 2 || len(inserter.calls) != 2 {
		t.Fatalf("enqueued=%d calls=%d, want 2/2", enqueued, len(inserter.calls))
	}

	if inserter.calls[0].FeedbackRecordID != firstID ||
		inserter.calls[0].TargetLang != "de-DE" || inserter.calls[0].ValueTextHash != "backfill:run-1" {
		t.Fatalf("first job = %+v, want firstID/de-DE/backfill:run-1", inserter.calls[0])
	}
}

func TestFeedbackRecordsService_BackfillTranslationsForTenant_RepoError(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{tenantBackfillErr: errors.New("db down")}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	_, err := svc.BackfillTranslationsForTenant(
		context.Background(), &mockTranslationInserter{}, TranslationsQueueName, 3, "org-1", "run-1")
	if err == nil {
		t.Fatal("BackfillTranslationsForTenant() = nil error, want repo error")
	}
}

func TestFeedbackRecordsService_BackfillTranslations(t *testing.T) {
	id1 := uuid.Must(uuid.NewV7())
	id2 := uuid.Must(uuid.NewV7())
	repo := &mockFeedbackRecordsRepo{
		translationBackfillTargets: []models.TranslationBackfillTarget{
			{FeedbackRecordID: id1, TargetLang: "de-DE"},
			{FeedbackRecordID: id2, TargetLang: "fr-FR"},
		},
	}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")
	inserter := &mockTranslationInserter{}

	enqueued, err := svc.BackfillTranslations(context.Background(), inserter, TranslationsQueueName, 3, "run-1")
	if err != nil {
		t.Fatalf("BackfillTranslations() error = %v", err)
	}

	if enqueued != 2 || len(inserter.calls) != 2 {
		t.Fatalf("enqueued = %d, inserter calls = %d, want 2 and 2", enqueued, len(inserter.calls))
	}

	first := inserter.calls[0]
	if first.FeedbackRecordID != id1 || first.TargetLang != "de-DE" || first.ValueTextHash != "backfill:run-1" {
		t.Fatalf("first job = %+v, want {%s de-DE backfill:run-1}", first, id1)
	}
}

func TestFeedbackRecordsService_BackfillTranslations_RepoError(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{translationBackfillErr: errors.New("boom")}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	_, err := svc.BackfillTranslations(context.Background(), &mockTranslationInserter{}, TranslationsQueueName, 3, "run-1")
	if err == nil {
		t.Fatal("BackfillTranslations() = nil, want the repo error propagated")
	}
}

func TestFeedbackRecordsService_BackfillSentiment(t *testing.T) {
	id1 := uuid.Must(uuid.NewV7())
	id2 := uuid.Must(uuid.NewV7())
	repo := &mockFeedbackRecordsRepo{sentimentBackfillTargets: []uuid.UUID{id1, id2}}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")
	inserter := &recordingInserter{}

	enqueued, err := svc.BackfillSentiment(context.Background(), inserter, SentimentsQueueName, 3, "run-1")
	if err != nil {
		t.Fatalf("BackfillSentiment() error = %v", err)
	}

	if enqueued != 2 || len(inserter.args) != 2 {
		t.Fatalf("enqueued=%d calls=%d, want 2/2", enqueued, len(inserter.args))
	}

	first, ok := inserter.args[0].(FeedbackSentimentArgs)
	if !ok {
		t.Fatalf("first job type = %T, want FeedbackSentimentArgs", inserter.args[0])
	}

	if first.FeedbackRecordID != id1 || first.ValueTextHash != "backfill:run-1" {
		t.Fatalf("first job = %+v, want {%s backfill:run-1}", first, id1)
	}

	// Backfill jobs dedupe by (record, run) within the window.
	if !inserter.opts[0].UniqueOpts.ByArgs || inserter.opts[0].UniqueOpts.ByPeriod != classifyBackfillUniquePeriod {
		t.Fatalf("insert opts UniqueOpts = %+v, want ByArgs + classifyBackfillUniquePeriod", inserter.opts[0].UniqueOpts)
	}
}

func TestFeedbackRecordsService_BackfillSentiment_RepoError(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{sentimentBackfillErr: errors.New("db down")}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	if _, err := svc.BackfillSentiment(context.Background(), &recordingInserter{}, SentimentsQueueName, 3, "run-1"); err == nil {
		t.Fatal("BackfillSentiment() = nil error, want the repo error propagated")
	}
}

func TestFeedbackRecordsService_BackfillEmotions(t *testing.T) {
	id1 := uuid.Must(uuid.NewV7())
	id2 := uuid.Must(uuid.NewV7())
	repo := &mockFeedbackRecordsRepo{emotionsBackfillTargets: []uuid.UUID{id1, id2}}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")
	inserter := &recordingInserter{}

	enqueued, err := svc.BackfillEmotions(context.Background(), inserter, EmotionsQueueName, 3, "run-1")
	if err != nil {
		t.Fatalf("BackfillEmotions() error = %v", err)
	}

	if enqueued != 2 || len(inserter.args) != 2 {
		t.Fatalf("enqueued=%d calls=%d, want 2/2", enqueued, len(inserter.args))
	}

	first, ok := inserter.args[0].(FeedbackEmotionsArgs)
	if !ok {
		t.Fatalf("first job type = %T, want FeedbackEmotionsArgs", inserter.args[0])
	}

	if first.FeedbackRecordID != id1 || first.ValueTextHash != "backfill:run-1" {
		t.Fatalf("first job = %+v, want {%s backfill:run-1}", first, id1)
	}
}

func TestFeedbackRecordsService_BackfillEmotions_RepoError(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{emotionsBackfillErr: errors.New("boom")}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	if _, err := svc.BackfillEmotions(context.Background(), &recordingInserter{}, EmotionsQueueName, 3, "run-1"); err == nil {
		t.Fatal("BackfillEmotions() = nil, want the repo error propagated")
	}
}

func assertDeletedEventDataAt(
	t *testing.T,
	publisher *capturePublisher,
	index int,
	eventType datatypes.EventType,
	tenantID string,
	ids []uuid.UUID,
) {
	t.Helper()

	if publisher.callCount <= index {
		t.Fatalf("published %d events, want event at index %d", publisher.callCount, index)
	}

	event := publisher.events[index]
	if event.eventType != eventType {
		t.Fatalf("published event type = %s, want %s", event.eventType, eventType)
	}

	data, ok := event.data.(models.DeletedIDsEventData)
	if !ok {
		t.Fatalf("published data type = %T, want DeletedIDsEventData", event.data)
	}

	if data.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", data.TenantID, tenantID)
	}

	if len(data.IDs) != len(ids) {
		t.Fatalf("IDs length = %d, want %d", len(data.IDs), len(ids))
	}

	for i := range ids {
		if data.IDs[i] != ids[i] {
			t.Errorf("IDs[%d] = %v, want %v", i, data.IDs[i], ids[i])
		}
	}
}

func assertDeletedEventData(
	t *testing.T, publisher *capturePublisher, eventType datatypes.EventType, tenantID string, ids []uuid.UUID,
) {
	t.Helper()

	if publisher.callCount != 1 || publisher.eventType != eventType {
		t.Fatalf("published event = (%d, %s), want one %s", publisher.callCount, publisher.eventType, eventType)
	}

	data, ok := publisher.data.(models.DeletedIDsEventData)
	if !ok {
		t.Fatalf("published data type = %T, want DeletedIDsEventData", publisher.data)
	}

	if data.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", data.TenantID, tenantID)
	}

	if len(data.IDs) != len(ids) {
		t.Fatalf("IDs length = %d, want %d", len(data.IDs), len(ids))
	}

	for i := range ids {
		if data.IDs[i] != ids[i] {
			t.Errorf("IDs[%d] = %v, want %v", i, data.IDs[i], ids[i])
		}
	}
}

// TestFeedbackRecordsService_UpdateFeedbackRecord_IdempotentResendPublishesNoEvent locks the
// comparison-based event contract: a PATCH whose set fields all equal the record's current values
// (an integration idempotently re-sending state) publishes NO update event — so webhooks are not
// re-fired and no enrichment re-runs on unchanged content.
func TestFeedbackRecordsService_UpdateFeedbackRecord_IdempotentResendPublishesNoEvent(t *testing.T) {
	text := "same text"
	current := &models.FeedbackRecord{
		ID:        uuid.Must(uuid.NewV7()),
		FieldType: models.FieldTypeText,
		ValueText: &text,
	}
	repo := &mockFeedbackRecordsRepo{record: current}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	resend := "same text"

	_, err := svc.UpdateFeedbackRecord(context.Background(), current.ID, &models.UpdateFeedbackRecordRequest{
		ValueText: &resend,
	})
	if err != nil {
		t.Fatalf("UpdateFeedbackRecord() error = %v", err)
	}

	if publisher.callCount != 0 {
		t.Fatalf("published %d event(s) for an idempotent re-send, want 0", publisher.callCount)
	}
}

// TestFeedbackRecordsService_UpdateFeedbackRecord_RealChangePublishesChangedFields locks the
// complement: a PATCH that actually changes a field publishes the update event carrying exactly
// the fields that changed.
func TestFeedbackRecordsService_UpdateFeedbackRecord_RealChangePublishesChangedFields(t *testing.T) {
	oldText := "old text"
	lang := "en"
	current := &models.FeedbackRecord{
		ID:        uuid.Must(uuid.NewV7()),
		FieldType: models.FieldTypeText,
		ValueText: &oldText,
		Language:  &lang,
	}
	repo := &mockFeedbackRecordsRepo{record: current}
	publisher := &capturePublisher{}
	svc := NewFeedbackRecordsService(repo, nil, "", publisher, nil, "", 0, "")

	newText := "new text"
	sameLang := "en"

	_, err := svc.UpdateFeedbackRecord(context.Background(), current.ID, &models.UpdateFeedbackRecordRequest{
		ValueText: &newText,
		Language:  &sameLang, // present but unchanged: must not appear in ChangedFields
	})
	if err != nil {
		t.Fatalf("UpdateFeedbackRecord() error = %v", err)
	}

	if publisher.callCount != 1 || publisher.eventType != datatypes.FeedbackRecordUpdated {
		t.Fatalf("published (%d, %s), want one feedback_record.updated", publisher.callCount, publisher.eventType)
	}

	if len(publisher.changedFields) != 1 || publisher.changedFields[0] != "value_text" {
		t.Fatalf("changedFields = %v, want [value_text] (only what actually changed)", publisher.changedFields)
	}
}

// TestFeedbackRecordsService_BackfillTranslations_CountsUniqueSkipsSeparately locks the truthful
// enqueue accounting: a unique-skipped duplicate is not reported as enqueued.
func TestFeedbackRecordsService_BackfillTranslations_CountsUniqueSkipsSeparately(t *testing.T) {
	repo := &mockFeedbackRecordsRepo{translationBackfillTargets: []models.TranslationBackfillTarget{
		{FeedbackRecordID: uuid.Must(uuid.NewV7()), TargetLang: "de-DE"},
		{FeedbackRecordID: uuid.Must(uuid.NewV7()), TargetLang: "de-DE"},
	}}
	svc := NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	inserter := &mockTranslationInserter{skipEvery: 2} // every 2nd insert reports UniqueSkippedAsDuplicate

	enqueued, err := svc.BackfillTranslations(context.Background(), inserter, TranslationsQueueName, 3, "run-1")
	if err != nil {
		t.Fatalf("BackfillTranslations() error = %v", err)
	}

	if enqueued != 1 {
		t.Fatalf("enqueued = %d, want 1 (the duplicate is skipped, not counted)", enqueued)
	}
}
