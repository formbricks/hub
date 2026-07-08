package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

// TestFeedbackRecords_ValueIDRoundTrip locks the create/read/update paths for
// value_id: a categorical answer can carry both value_text (display label) and
// value_id (stable key), both round-trip through the shared scanFeedbackRecord, an update
// changes value_id, and an unrelated update leaves it intact.
func TestFeedbackRecords_ValueIDRoundTrip(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)

	tenantID := testTenantID("value-id")
	valueText := "Very satisfied"
	valueID := "opt_very_satisfied"

	created, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "csat",
		FieldType:    models.FieldTypeCategorical,
		ValueText:    &valueText,
		ValueID:      &valueID,
		TenantID:     tenantID,
		SubmissionID: testTenantID("submission"),
	})
	require.NoError(t, err)
	require.NotNil(t, created.ValueID)
	assert.Equal(t, valueID, *created.ValueID)
	require.NotNil(t, created.ValueText)
	assert.Equal(t, valueText, *created.ValueText)

	// Round-trips via GetByID (shared scan path).
	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ValueID)
	assert.Equal(t, valueID, *got.ValueID)

	// Update changes value_id.
	newID := "opt_neutral"
	updated, _, err := repo.Update(ctx, created.ID, &models.UpdateFeedbackRecordRequest{ValueID: &newID})
	require.NoError(t, err)
	require.NotNil(t, updated.ValueID)
	assert.Equal(t, newID, *updated.ValueID)

	// An unrelated update leaves value_id intact.
	newLabel := "Neutral"
	afterUnrelated, _, err := repo.Update(ctx, created.ID, &models.UpdateFeedbackRecordRequest{ValueText: &newLabel})
	require.NoError(t, err)
	require.NotNil(t, afterUnrelated.ValueID)
	assert.Equal(t, newID, *afterUnrelated.ValueID, "value_id survives an unrelated update")
}

// TestFeedbackRecords_ListFilterByValueID locks the value_id list filter (the "update all records
// for option X" flow): listing a tenant filtered by value_id returns only the records carrying that
// option id, across submissions.
func TestFeedbackRecords_ListFilterByValueID(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)

	tenantID := testTenantID("value-id-filter")
	label := "Very satisfied"
	wantID := "opt_very_satisfied"
	otherID := "opt_neutral"

	// Two records for the target option (distinct submissions) and one for another option.
	createOption := func(submission, optionID string) {
		_, createErr := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "csat",
			FieldType:    models.FieldTypeCategorical,
			ValueText:    &label,
			ValueID:      &optionID,
			TenantID:     tenantID,
			SubmissionID: submission,
		})
		require.NoError(t, createErr)
	}
	createOption(testTenantID("sub-a"), wantID)
	createOption(testTenantID("sub-b"), wantID)
	createOption(testTenantID("sub-c"), otherID)

	records, _, err := repo.List(ctx, &models.ListFeedbackRecordsFilters{TenantID: &tenantID, ValueID: &wantID})
	require.NoError(t, err)
	assert.Len(t, records, 2, "only the two records with the target value_id are returned")

	for _, rec := range records {
		require.NotNil(t, rec.ValueID)
		assert.Equal(t, wantID, *rec.ValueID)
	}
}
