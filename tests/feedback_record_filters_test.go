package tests

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/cursor"
	"github.com/formbricks/hub/pkg/database"
)

// filterTestEnv is a repository bound to a tenant nobody else in the suite writes to, so the
// assertions can compare exact sets rather than "contains".
type filterTestEnv struct {
	repo   *repository.FeedbackRecordsRepository
	db     *pgxpool.Pool
	tenant string
}

func newFilterTestEnv(t *testing.T, suffix string) *filterTestEnv {
	t.Helper()

	ctx := t.Context()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	t.Cleanup(db.Close)

	return &filterTestEnv{
		repo:   repository.NewFeedbackRecordsRepository(db),
		db:     db,
		tenant: "filters-" + suffix + "-" + uuid.NewString(),
	}
}

// seed creates one record, applying opts before insert. Defaults are a plain text answer.
func (e *filterTestEnv) seed(t *testing.T, opts ...func(*models.CreateFeedbackRecordRequest)) *models.FeedbackRecord {
	t.Helper()

	valueText := "some feedback"
	req := &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &valueText,
		TenantID:     e.tenant,
		SubmissionID: uuid.NewString(),
	}

	for _, opt := range opts {
		opt(req)
	}

	created, err := e.repo.Create(t.Context(), req)
	require.NoError(t, err)

	return created
}

// list runs a filtered listing scoped to this env's tenant and returns the matching IDs.
func (e *filterTestEnv) list(t *testing.T, apply func(*models.ListFeedbackRecordsFilters)) []uuid.UUID {
	t.Helper()

	tenant := e.tenant
	filters := &models.ListFeedbackRecordsFilters{TenantID: &tenant, Limit: 1000}

	if apply != nil {
		apply(filters)
	}

	records, _, err := e.repo.List(t.Context(), filters)
	require.NoError(t, err)

	ids := make([]uuid.UUID, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}

	return ids
}

// count runs the same filters through the count path.
func (e *filterTestEnv) count(t *testing.T, apply func(*models.ListFeedbackRecordsFilters)) int {
	t.Helper()

	tenant := e.tenant
	filters := &models.ListFeedbackRecordsFilters{TenantID: &tenant}

	if apply != nil {
		apply(filters)
	}

	total, err := e.repo.Count(t.Context(), filters)
	require.NoError(t, err)

	return total
}

// TestFeedbackRecordFilters_FieldTypeMultiValue exercises the one filter whose column is a
// PostgreSQL ENUM. The generated SQL casts through ::text[]::field_type_enum[], and only a real
// database can confirm the cast is valid and still selects the right rows.
func TestFeedbackRecordFilters_FieldTypeMultiValue(t *testing.T) {
	env := newFilterTestEnv(t, "field-type")

	number := 7.0
	text := env.seed(t)
	rating := env.seed(t, func(r *models.CreateFeedbackRecordRequest) {
		r.FieldType = models.FieldTypeRating
		r.ValueText = nil
		r.ValueNumber = &number
	})
	env.seed(t, func(r *models.CreateFeedbackRecordRequest) {
		r.FieldType = models.FieldTypeNPS
		r.ValueText = nil
		r.ValueNumber = &number
	})

	got := env.list(t, func(f *models.ListFeedbackRecordsFilters) {
		f.FieldType = []models.FieldType{models.FieldTypeText, models.FieldTypeRating}
	})

	assert.ElementsMatch(t, []uuid.UUID{text.ID, rating.ID}, got)
}

// TestFeedbackRecordFilters_SingleValueBackwardCompatible verifies each converted filter still
// selects exactly what it did as a scalar. `col = ANY($1)` over a one-element array must be
// indistinguishable from `col = $1`.
func TestFeedbackRecordFilters_SingleValueBackwardCompatible(t *testing.T) {
	env := newFilterTestEnv(t, "single-value")

	wanted := env.seed(t, func(r *models.CreateFeedbackRecordRequest) {
		r.SourceType = "survey"
		r.SourceID = new("src-1")
		r.SourceName = new("After match")
		r.FieldID = "q-wanted"
		r.FieldGroupID = new("grp-1")
		r.ValueID = new("opt-a")
		r.UserID = new("user-1")
		r.Language = new("en")
		r.SubmissionID = "sub-wanted"
	})

	env.seed(t, func(r *models.CreateFeedbackRecordRequest) {
		r.SourceType = "review"
		r.SourceID = new("src-2")
		r.SourceName = new("Before match")
		r.FieldID = "q-other"
		r.FieldGroupID = new("grp-2")
		r.ValueID = new("opt-b")
		r.UserID = new("user-2")
		r.Language = new("ar")
		r.SubmissionID = "sub-other"
	})

	tests := []struct {
		name  string
		apply func(*models.ListFeedbackRecordsFilters)
	}{
		{"source_type", func(f *models.ListFeedbackRecordsFilters) { f.SourceType = []string{"survey"} }},
		{"source_id", func(f *models.ListFeedbackRecordsFilters) { f.SourceID = []string{"src-1"} }},
		{"source_name", func(f *models.ListFeedbackRecordsFilters) { f.SourceName = []string{"After match"} }},
		{"field_id", func(f *models.ListFeedbackRecordsFilters) { f.FieldID = []string{"q-wanted"} }},
		{"field_group_id", func(f *models.ListFeedbackRecordsFilters) { f.FieldGroupID = []string{"grp-1"} }},
		{"value_id", func(f *models.ListFeedbackRecordsFilters) { f.ValueID = []string{"opt-a"} }},
		{"user_id", func(f *models.ListFeedbackRecordsFilters) { f.UserID = []string{"user-1"} }},
		{"submission_id", func(f *models.ListFeedbackRecordsFilters) { f.SubmissionID = []string{"sub-wanted"} }},
		{"language", func(f *models.ListFeedbackRecordsFilters) { f.Language = []string{"en"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, []uuid.UUID{wanted.ID}, env.list(t, tt.apply))
		})
	}

	t.Run("multi-value ORs within one filter", func(t *testing.T) {
		got := env.list(t, func(f *models.ListFeedbackRecordsFilters) {
			f.SourceType = []string{"survey", "review"}
		})
		assert.Len(t, got, 2)
	})
}

// TestFeedbackRecordFilters_EmotionsMatchAny is the entire specification of ANY-vs-ALL semantics.
// A containment implementation returns only the record carrying both labels; overlap returns both.
func TestFeedbackRecordFilters_EmotionsMatchAny(t *testing.T) {
	env := newFilterTestEnv(t, "emotions")

	joyOnly := env.seed(t)
	joyAndAnger := env.seed(t)
	sadOnly := env.seed(t)

	env.setEmotions(t, joyOnly.ID, models.EmotionJoy)
	env.setEmotions(t, joyAndAnger.ID, models.EmotionJoy, models.EmotionAnger)
	env.setEmotions(t, sadOnly.ID, models.EmotionSadness)

	got := env.list(t, func(f *models.ListFeedbackRecordsFilters) {
		f.Emotions = []models.EmotionValue{models.EmotionJoy, models.EmotionAnger}
	})

	assert.ElementsMatch(t, []uuid.UUID{joyOnly.ID, joyAndAnger.ID}, got,
		"emotions must match ANY of the requested labels, not ALL of them")
}

// TestFeedbackRecordFilters_BoundsAreInclusive pins the boundary row into the result for every
// range filter. An off-by-one on an inclusive bound is silent and easy to introduce.
func TestFeedbackRecordFilters_BoundsAreInclusive(t *testing.T) {
	env := newFilterTestEnv(t, "bounds")

	collected := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	number := 5.0

	boundary := env.seed(t, func(r *models.CreateFeedbackRecordRequest) {
		r.CollectedAt = &collected
		r.FieldType = models.FieldTypeRating
		r.ValueText = nil
		r.ValueNumber = &number
		r.ValueDate = &collected
	})

	env.setSentimentScore(t, boundary.ID, models.SentimentNeutral, 0.25)

	tests := []struct {
		name  string
		apply func(*models.ListFeedbackRecordsFilters)
	}{
		{"since on the boundary", func(f *models.ListFeedbackRecordsFilters) { f.Since = &collected }},
		{"until on the boundary", func(f *models.ListFeedbackRecordsFilters) { f.Until = &collected }},
		{"value_number_min on the boundary", func(f *models.ListFeedbackRecordsFilters) { f.ValueNumberMin = &number }},
		{"value_number_max on the boundary", func(f *models.ListFeedbackRecordsFilters) { f.ValueNumberMax = &number }},
		{"value_date_min on the boundary", func(f *models.ListFeedbackRecordsFilters) { f.ValueDateMin = &collected }},
		{"value_date_max on the boundary", func(f *models.ListFeedbackRecordsFilters) { f.ValueDateMax = &collected }},
		{
			"sentiment_score_min on the boundary",
			func(f *models.ListFeedbackRecordsFilters) { f.SentimentScoreMin = new(0.25) },
		},
		{
			"sentiment_score_max on the boundary",
			func(f *models.ListFeedbackRecordsFilters) { f.SentimentScoreMax = new(0.25) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, []uuid.UUID{boundary.ID}, env.list(t, tt.apply))
		})
	}
}

// TestFeedbackRecordFilters_CreatedAtIsDistinctFromCollectedAt is the test that catches someone
// collapsing the two timestamp pairs into one. They diverge on a historical re-import: an old
// collected_at with today's created_at.
func TestFeedbackRecordFilters_CreatedAtIsDistinctFromCollectedAt(t *testing.T) {
	env := newFilterTestEnv(t, "created-at")

	oldCollected := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	backfilled := env.seed(t, func(r *models.CreateFeedbackRecordRequest) { r.CollectedAt = &oldCollected })

	// created_at is server-assigned at insert, so this record is old by collected_at and new by
	// created_at — exactly the re-import shape.
	require.True(t, backfilled.CreatedAt.After(oldCollected.AddDate(1, 0, 0)))

	cutoff := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	assert.Empty(t, env.list(t, func(f *models.ListFeedbackRecordsFilters) { f.Since = &cutoff }),
		"since bounds collected_at, which is before the cutoff")

	assert.Equal(t, []uuid.UUID{backfilled.ID},
		env.list(t, func(f *models.ListFeedbackRecordsFilters) { f.CreatedSince = &cutoff }),
		"created_since bounds created_at, which is after the cutoff")
}

// dbNow reads the database clock, the one every writer stamps updated_at from. A cutoff taken from
// the test process's clock would make these tests depend on the two clocks agreeing.
func (e *filterTestEnv) dbNow(t *testing.T) time.Time {
	t.Helper()

	var now time.Time
	require.NoError(t, e.db.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&now))

	return now
}

// TestFeedbackRecordFilters_UpdatedSinceSelectsCreatedAndChanged drives each real write path the
// docs promise stamps updated_at — insert, PATCH, and the sentiment, emotions and translation
// results — and checks updated_since selects exactly those records, while an untouched older record
// stays out (ENG-3420).
func TestFeedbackRecordFilters_UpdatedSinceSelectsCreatedAndChanged(t *testing.T) {
	env := newFilterTestEnv(t, "updated-since")

	untouched := env.seed(t)
	patched := env.seed(t)
	withSentiment := env.seed(t)
	withEmotions := env.seed(t)
	translated := env.seed(t)

	cutoff := env.dbNow(t)

	updated, _, err := env.repo.Update(t.Context(), patched.ID, &models.UpdateFeedbackRecordRequest{
		UserID: new("user-after-cutoff"),
	})
	require.NoError(t, err)
	require.False(t, updated.UpdatedAt.Before(cutoff), "PATCH must move updated_at past the cutoff")

	sentiment, score := models.SentimentPositive, 0.5
	require.NoError(t, env.repo.SetSentiment(t.Context(), withSentiment.ID, &sentiment, &score, nil))
	require.NoError(t, env.repo.SetEmotions(t.Context(), withEmotions.ID, []models.EmotionValue{models.EmotionJoy}, nil))

	// No tenant settings row, so the effective target is the default passed here and the write lands.
	translation := "etwas Feedback"
	require.NoError(t, env.repo.SetTranslation(t.Context(), translated.ID, &translation, "de", "de", nil))

	created := env.seed(t)

	want := []uuid.UUID{patched.ID, withSentiment.ID, withEmotions.ID, translated.ID, created.ID}
	since := func(f *models.ListFeedbackRecordsFilters) { f.UpdatedSince = &cutoff }
	assert.ElementsMatch(t, want, env.list(t, since),
		"updated_since selects records created or changed at or after the cutoff")
	assert.Equal(t, len(want), env.count(t, since), "count must describe the same set")

	until := func(f *models.ListFeedbackRecordsFilters) { f.UpdatedUntil = &cutoff }
	assert.Equal(t, []uuid.UUID{untouched.ID}, env.list(t, until),
		"updated_until bounds updated_at, so only the record nobody touched is at or before the cutoff")
}

// TestFeedbackRecordFilters_UpdatedSincePaginationSurvivesUpdates is the reason updated_at is a
// filter and never a sort key. Records keep changing while a sync pages through them; ordering by
// an immutable column means an update moves a record's updated_at but never its position, so the
// traversal still returns every record exactly once.
func TestFeedbackRecordFilters_UpdatedSincePaginationSurvivesUpdates(t *testing.T) {
	env := newFilterTestEnv(t, "updated-since-pages")
	svc := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(env.db), nil, "", nil, nil, "", 0, "",
	)

	// Written before the cutoff, so it must never appear: without it this test would pass with
	// updated_since ignored altogether.
	before := env.seed(t)
	cutoff := env.dbNow(t)

	const records, pageLimit = 6, 2

	want := make(map[uuid.UUID]bool, records)
	ordered := make([]uuid.UUID, 0, records)

	for range records {
		record := env.seed(t)
		want[record.ID] = true
		ordered = append(ordered, record.ID)
	}

	touch := func(id uuid.UUID) {
		_, _, err := env.repo.Update(t.Context(), id, &models.UpdateFeedbackRecordRequest{
			UserID: new("touched-" + uuid.NewString()),
		})
		require.NoError(t, err)
	}

	seen := make(map[uuid.UUID]int, records)
	nextCursor := ""

	for page := range records {
		tenant := env.tenant
		resp, err := svc.ListFeedbackRecords(t.Context(), &models.ListFeedbackRecordsFilters{
			TenantID:     &tenant,
			UpdatedSince: &cutoff,
			Sort:         models.SortFieldCreatedAt,
			Order:        models.SortOrderAsc,
			Limit:        pageLimit,
			Cursor:       nextCursor,
		})
		require.NoError(t, err, "page %d", page)

		for _, record := range resp.Data {
			seen[record.ID]++
		}

		if page == 0 {
			// Change one record already returned and one not reached yet. Under sort=updated_at
			// the second would jump behind the cursor and be skipped.
			touch(ordered[0])
			touch(ordered[records-1])
		}

		nextCursor = resp.NextCursor
		if nextCursor == "" {
			break
		}
	}

	require.Empty(t, nextCursor, "pagination did not terminate")
	assert.NotContains(t, seen, before.ID, "a record last written before updated_since must not be returned")
	assert.Len(t, seen, records, "every record must be returned")

	for id, times := range seen {
		assert.True(t, want[id], "record %s does not belong to this traversal", id)
		assert.Equal(t, 1, times, "record %s returned %d times", id, times)
	}
}

// TestFeedbackRecordFilters_UpdatedSinceWatermarkFromRunStart drives the sync procedure the docs
// prescribe across two runs, in the one shape that makes the choice of watermark matter: a record
// changes after the run has paged past it, while a record further along changes later still.
//
// The newest updated_at the run received is then AFTER the first record's change, so a watermark
// taken from the results would skip that change for good. The watermark the docs prescribe — the
// run's start — is before it, and the second run returns it.
func TestFeedbackRecordFilters_UpdatedSinceWatermarkFromRunStart(t *testing.T) {
	env := newFilterTestEnv(t, "updated-since-watermark")
	svc := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(env.db), nil, "", nil, nil, "", 0, "",
	)

	first := env.seed(t)
	env.seed(t)
	last := env.seed(t)

	touch := func(id uuid.UUID) {
		_, _, err := env.repo.Update(t.Context(), id, &models.UpdateFeedbackRecordRequest{
			UserID: new("touched-" + uuid.NewString()),
		})
		require.NoError(t, err)
	}

	// run pages through every record updated at or after since, one per page, calling between
	// after the first page. It returns the records in the order received.
	run := func(since time.Time, between func()) []*models.FeedbackRecord {
		var (
			received   []*models.FeedbackRecord
			nextCursor string
		)

		for page := range 10 {
			tenant := env.tenant
			resp, err := svc.ListFeedbackRecords(t.Context(), &models.ListFeedbackRecordsFilters{
				TenantID: &tenant, UpdatedSince: &since,
				Sort: models.SortFieldCreatedAt, Order: models.SortOrderAsc,
				Limit: 1, Cursor: nextCursor,
			})
			require.NoError(t, err)

			for i := range resp.Data {
				received = append(received, &resp.Data[i])
			}

			if page == 0 && between != nil {
				between()
			}

			nextCursor = resp.NextCursor
			if nextCursor == "" {
				return received
			}
		}

		t.Fatal("pagination did not terminate")

		return nil
	}

	runStart := env.dbNow(t)
	firstRun := run(time.Time{}, func() {
		touch(first.ID) // already paged past
		touch(last.ID)  // not reached yet, changed later still
	})
	require.Len(t, firstRun, 3)
	require.Equal(t, first.ID, firstRun[0].ID)

	newestReceived := firstRun[0].UpdatedAt
	for _, record := range firstRun {
		if record.UpdatedAt.After(newestReceived) {
			newestReceived = record.UpdatedAt
		}
	}

	var firstChangedAt time.Time
	require.NoError(t, env.db.QueryRow(t.Context(),
		`SELECT updated_at FROM feedback_records WHERE id = $1`, first.ID).Scan(&firstChangedAt))

	// The scenario is real: a results-derived watermark would already be past the first change.
	require.True(t, firstChangedAt.Before(newestReceived),
		"the first record's change (%v) must be older than the newest updated_at received (%v)",
		firstChangedAt, newestReceived)

	secondRun := run(runStart, nil)

	ids := make([]uuid.UUID, 0, len(secondRun))
	for _, record := range secondRun {
		ids = append(ids, record.ID)
	}

	assert.Contains(t, ids, first.ID, "a run-start watermark must return the change made behind the cursor")
}

// TestFeedbackRecordFilters_PresencePartitions verifies each presence filter reads the column it
// is supposed to, and that true/false partition the tenant exactly.
//
// The fixture gives every filter a distinct partition, which is what makes it able to catch a
// predicate pointed at the wrong column. With fewer records two of the three filters return
// identical sets and stay silently swappable:
//
//	record          sentiment  emotions  translation
//	all                 ✓         ✓           ✓
//	noTranslation       ✓         ✓           ·
//	sentimentOnly       ✓         ·           ·
//	plain               ·         ·           ·
func TestFeedbackRecordFilters_PresencePartitions(t *testing.T) {
	env := newFilterTestEnv(t, "presence")

	all := env.seed(t)
	noTranslation := env.seed(t)
	sentimentOnly := env.seed(t)
	plain := env.seed(t)

	env.setSentimentScore(t, all.ID, models.SentimentPositive, 0.8)
	env.setEmotions(t, all.ID, models.EmotionJoy)
	env.setTranslation(t, all.ID, "en")

	env.setSentimentScore(t, noTranslation.ID, models.SentimentNegative, -0.4)
	env.setEmotions(t, noTranslation.ID, models.EmotionAnger)

	env.setSentimentScore(t, sentimentOnly.ID, models.SentimentNeutral, 0)

	present, absent := true, false

	tests := []struct {
		name        string
		set         func(*models.ListFeedbackRecordsFilters, *bool)
		wantWith    []uuid.UUID
		wantWithout []uuid.UUID
	}{
		{
			name:     "has_sentiment",
			set:      func(f *models.ListFeedbackRecordsFilters, v *bool) { f.HasSentiment = v },
			wantWith: []uuid.UUID{all.ID, noTranslation.ID, sentimentOnly.ID}, wantWithout: []uuid.UUID{plain.ID},
		},
		{
			name:     "has_emotions",
			set:      func(f *models.ListFeedbackRecordsFilters, v *bool) { f.HasEmotions = v },
			wantWith: []uuid.UUID{all.ID, noTranslation.ID}, wantWithout: []uuid.UUID{sentimentOnly.ID, plain.ID},
		},
		{
			name:     "has_translation",
			set:      func(f *models.ListFeedbackRecordsFilters, v *bool) { f.HasTranslation = v },
			wantWith: []uuid.UUID{all.ID}, wantWithout: []uuid.UUID{noTranslation.ID, sentimentOnly.ID, plain.ID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			with := env.list(t, func(f *models.ListFeedbackRecordsFilters) { tt.set(f, &present) })
			without := env.list(t, func(f *models.ListFeedbackRecordsFilters) { tt.set(f, &absent) })

			assert.ElementsMatch(t, tt.wantWith, with)
			assert.ElementsMatch(t, tt.wantWithout, without)
			assert.Len(t, append(with, without...), env.count(t, nil), "the two sides must partition the tenant")
		})
	}
}

// TestFeedbackRecordFilters_CountAgreesWithList verifies the two endpoints cannot describe
// different sets. They share buildFilterConditions; this is what proves it stayed shared.
func TestFeedbackRecordFilters_CountAgreesWithList(t *testing.T) {
	env := newFilterTestEnv(t, "count-parity")

	for i := range 3 {
		sourceType := "survey"
		if i == 2 {
			sourceType = "review"
		}

		env.seed(t, func(r *models.CreateFeedbackRecordRequest) { r.SourceType = sourceType })
	}

	tests := []struct {
		name  string
		apply func(*models.ListFeedbackRecordsFilters)
	}{
		{"unfiltered", nil},
		{"source_type", func(f *models.ListFeedbackRecordsFilters) { f.SourceType = []string{"survey"} }},
		{"multi source_type", func(f *models.ListFeedbackRecordsFilters) { f.SourceType = []string{"survey", "review"} }},
		{"matches nothing", func(f *models.ListFeedbackRecordsFilters) { f.SourceType = []string{"nope"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Len(t, env.list(t, tt.apply), env.count(t, tt.apply))
		})
	}
}

// TestFeedbackRecordFilters_TenantIsolation runs every new filter against two tenants holding
// identical data. Converting eight filters to `= ANY` is exactly the kind of change where a
// dropped tenant predicate would go unnoticed, because every filtered query still returns
// plausible rows.
func TestFeedbackRecordFilters_TenantIsolation(t *testing.T) {
	mine := newFilterTestEnv(t, "isolation-mine")
	theirs := newFilterTestEnv(t, "isolation-theirs")

	score := 0.75
	collected := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	seedIdentical := func(env *filterTestEnv) *models.FeedbackRecord {
		record := env.seed(t, func(r *models.CreateFeedbackRecordRequest) {
			r.CollectedAt = &collected
			r.SourceType = "survey"
			r.SourceID = new("src-shared")
			r.SourceName = new("Shared source")
			r.FieldID = "q-shared"
			r.UserID = new("user-shared")
			r.Language = new("en")
		})
		env.setSentimentScore(t, record.ID, models.SentimentPositive, score)
		env.setEmotions(t, record.ID, models.EmotionJoy)

		return record
	}

	want := seedIdentical(mine)
	seedIdentical(theirs)

	tests := []struct {
		name  string
		apply func(*models.ListFeedbackRecordsFilters)
	}{
		{"source_type", func(f *models.ListFeedbackRecordsFilters) { f.SourceType = []string{"survey"} }},
		{"source_name", func(f *models.ListFeedbackRecordsFilters) { f.SourceName = []string{"Shared source"} }},
		{"user_id", func(f *models.ListFeedbackRecordsFilters) { f.UserID = []string{"user-shared"} }},
		{"language", func(f *models.ListFeedbackRecordsFilters) { f.Language = []string{"en"} }},
		{
			"field_type",
			func(f *models.ListFeedbackRecordsFilters) { f.FieldType = []models.FieldType{models.FieldTypeText} },
		},
		{
			"sentiment",
			func(f *models.ListFeedbackRecordsFilters) {
				f.Sentiment = []models.SentimentValue{models.SentimentPositive}
			},
		},
		{
			"emotions",
			func(f *models.ListFeedbackRecordsFilters) { f.Emotions = []models.EmotionValue{models.EmotionJoy} },
		},
		{"sentiment_score range", func(f *models.ListFeedbackRecordsFilters) { f.SentimentScoreMin = &score }},
		{"has_sentiment", func(f *models.ListFeedbackRecordsFilters) { f.HasSentiment = new(true) }},
		{"collected_at range", func(f *models.ListFeedbackRecordsFilters) { f.Since = &collected }},
		{"created_at range", func(f *models.ListFeedbackRecordsFilters) { f.CreatedSince = &collected }},
		{"updated_at range", func(f *models.ListFeedbackRecordsFilters) { f.UpdatedSince = &collected }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, []uuid.UUID{want.ID}, mine.list(t, tt.apply),
				"the filter must return only the requesting tenant's row")
		})
	}
}

// setEmotions writes emotion labels directly, bypassing the enrichment worker.
func (e *filterTestEnv) setEmotions(t *testing.T, id uuid.UUID, emotions ...models.EmotionValue) {
	t.Helper()

	labels := make([]string, 0, len(emotions))
	for _, emotion := range emotions {
		labels = append(labels, string(emotion))
	}

	_, err := e.db.Exec(t.Context(), `UPDATE feedback_records SET emotions = $2 WHERE id = $1`, id, labels)
	require.NoError(t, err)
}

// setTranslation writes translation output directly, bypassing the enrichment worker.
func (e *filterTestEnv) setTranslation(t *testing.T, id uuid.UUID, langKey string) {
	t.Helper()

	_, err := e.db.Exec(t.Context(),
		`UPDATE feedback_records SET value_text_translated = value_text, translation_lang_key = $2 WHERE id = $1`,
		id, langKey)
	require.NoError(t, err)
}

// setSentimentScore writes a sentiment label and score directly, bypassing the enrichment worker.
func (e *filterTestEnv) setSentimentScore(
	t *testing.T, id uuid.UUID, sentiment models.SentimentValue, score float64,
) {
	t.Helper()

	_, err := e.db.Exec(t.Context(),
		`UPDATE feedback_records SET sentiment = $2, sentiment_score = $3 WHERE id = $1`, id, sentiment, score)
	require.NoError(t, err)
}

// TestFeedbackRecordFilters_PaginationInvariant walks every (sort, order) combination end to end
// and asserts the traversal is a permutation of the seeded set: no row skipped, none repeated.
//
// The seed deliberately creates collected_at ties (five records share each of five timestamps),
// because that is the only shape where a broken tiebreak shows up. collected_at is client-supplied,
// so a submission POSTed with one timestamp across all its answers produces exactly this in
// production — ties are the normal case here, not an edge case.
func TestFeedbackRecordFilters_PaginationInvariant(t *testing.T) {
	env := newFilterTestEnv(t, "pagination")
	svc := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(env.db), nil, "", nil, nil, "", 0, "",
	)

	const (
		groups    = 5
		perGroup  = 5
		pageLimit = 4
	)

	want := make(map[uuid.UUID]bool, groups*perGroup)

	for group := range groups {
		collected := time.Date(2026, 5, 1+group, 12, 0, 0, 0, time.UTC)

		for range perGroup {
			// One insert per record, so created_at differs even where collected_at ties.
			want[env.seed(t, func(r *models.CreateFeedbackRecordRequest) { r.CollectedAt = &collected }).ID] = true
		}
	}

	orderings := []struct {
		sort  models.SortField
		order models.SortOrder
	}{
		{models.SortFieldCollectedAt, models.SortOrderDesc},
		{models.SortFieldCollectedAt, models.SortOrderAsc},
		{models.SortFieldCreatedAt, models.SortOrderDesc},
		{models.SortFieldCreatedAt, models.SortOrderAsc},
	}

	for _, ordering := range orderings {
		t.Run(string(ordering.sort)+" "+string(ordering.order), func(t *testing.T) {
			var (
				seen       []uuid.UUID
				nextCursor string
				sortValues []time.Time
			)

			// Bounded so a pagination bug fails as an assertion instead of hanging CI.
			maxPages := (groups*perGroup)/pageLimit + 3
			for page := range maxPages {
				tenant := env.tenant
				resp, err := svc.ListFeedbackRecords(t.Context(), &models.ListFeedbackRecordsFilters{
					TenantID: &tenant,
					Limit:    pageLimit,
					Sort:     ordering.sort,
					Order:    ordering.order,
					Cursor:   nextCursor,
				})
				require.NoError(t, err, "page %d", page)

				for _, record := range resp.Data {
					seen = append(seen, record.ID)
					sortValues = append(sortValues, record.SortValue(ordering.sort))
				}

				nextCursor = resp.NextCursor
				if nextCursor == "" {
					break
				}
			}

			require.Empty(t, nextCursor, "pagination did not terminate within %d pages", maxPages)

			unique := make(map[uuid.UUID]bool, len(seen))
			for _, id := range seen {
				require.False(t, unique[id], "record %s was returned on more than one page", id)
				unique[id] = true
			}

			assert.Len(t, seen, len(want), "the traversal must return every seeded record exactly once")
			assert.Equal(t, want, unique, "the traversal must be a permutation of the seeded set")

			// The concatenated pages must be globally ordered, which also proves the page
			// boundaries line up: the last row of page N and the first of page N+1 are adjacent.
			for i := 1; i < len(sortValues); i++ {
				if ordering.order == models.SortOrderDesc {
					assert.False(t, sortValues[i].After(sortValues[i-1]),
						"row %d (%v) sorts before row %d (%v) under DESC", i, sortValues[i], i-1, sortValues[i-1])

					continue
				}

				assert.False(t, sortValues[i].Before(sortValues[i-1]),
					"row %d (%v) sorts after row %d (%v) under ASC", i, sortValues[i], i-1, sortValues[i-1])
			}
		})
	}
}

// TestFeedbackRecordFilters_CursorIsBoundToItsOrdering covers the two halves of the guard: a
// cursor cannot cross orderings, and a cursor issued before sort control existed still works.
func TestFeedbackRecordFilters_CursorIsBoundToItsOrdering(t *testing.T) {
	env := newFilterTestEnv(t, "cursor-binding")
	svc := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(env.db), nil, "", nil, nil, "", 0, "",
	)

	for range 3 {
		env.seed(t)
	}

	tenant := env.tenant
	first, err := svc.ListFeedbackRecords(t.Context(), &models.ListFeedbackRecordsFilters{
		TenantID: &tenant, Limit: 1, Sort: models.SortFieldCollectedAt, Order: models.SortOrderDesc,
	})
	require.NoError(t, err)
	require.NotEmpty(t, first.NextCursor)

	t.Run("carrying it to another ordering is refused", func(t *testing.T) {
		_, err := svc.ListFeedbackRecords(t.Context(), &models.ListFeedbackRecordsFilters{
			TenantID: &tenant, Limit: 1, Sort: models.SortFieldCreatedAt, Order: models.SortOrderDesc,
			Cursor: first.NextCursor,
		})
		require.ErrorIs(t, err, cursor.ErrCursorSortMismatch)
	})

	// Re-encode without the ordering fields — byte-for-byte what a pre-sort-control Hub issued.
	key, err := cursor.DecodeKey(first.NextCursor)
	require.NoError(t, err)

	legacy, err := cursor.Encode(key.Timestamp, key.ID)
	require.NoError(t, err)

	t.Run("a legacy cursor is accepted under the default ordering", func(t *testing.T) {
		resp, err := svc.ListFeedbackRecords(t.Context(), &models.ListFeedbackRecordsFilters{
			TenantID: &tenant, Limit: 1, Cursor: legacy,
		})
		require.NoError(t, err)
		assert.Len(t, resp.Data, 1)
	})

	// It holds a collected_at position, so it is refused anywhere else rather than being compared
	// against created_at — the same hazard as an explicitly-tagged mismatch, just implicit.
	t.Run("a legacy cursor is refused under a non-default ordering", func(t *testing.T) {
		_, err := svc.ListFeedbackRecords(t.Context(), &models.ListFeedbackRecordsFilters{
			TenantID: &tenant, Limit: 1,
			Sort: models.SortFieldCreatedAt, Order: models.SortOrderDesc, Cursor: legacy,
		})
		require.ErrorIs(t, err, cursor.ErrCursorSortMismatch)
	})
}
