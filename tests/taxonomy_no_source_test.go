package tests

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

func setupTaxonomyNoSourceTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(context.Background(), cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	t.Cleanup(db.Close)

	return db
}

// TestTaxonomyNoSourceScope verifies that feedback records with a NULL/blank source_id
// participate in taxonomy through the canonical "no source" bucket (empty-string scope).
func TestTaxonomyNoSourceScope(t *testing.T) {
	ctx := context.Background()
	db := setupTaxonomyNoSourceTestDB(t)
	repo := repository.NewTaxonomyRepository(db)

	tenantID := "taxonomy-nosource-" + uuid.NewString()
	sourceType := "formbricks"
	fieldID := "feedback"

	const embeddingModel = "text-embedding-test"

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM taxonomy_runs WHERE tenant_id = $1`, tenantID)
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, tenantID)
	})

	// Two feedback records with no attributed source: one NULL, one blank.
	for _, srcID := range []any{nil, "   "} {
		_, err := db.Exec(ctx, `
			INSERT INTO feedback_records (
				source_type, source_id, field_id, field_label, field_type,
				value_text, tenant_id, submission_id
			)
			VALUES ($1, $2, $3, $4, $5::field_type_enum, $6, $7, $8)`,
			sourceType, srcID, fieldID, "Feedback", "text",
			"Login was confusing", tenantID, "submission-"+uuid.NewString(),
		)
		require.NoError(t, err)
	}

	// Discovery surfaces the records as a single "no source" bucket with empty SourceID.
	options, err := repo.ListFieldOptions(ctx, tenantID, embeddingModel)
	require.NoError(t, err)

	var noSource *models.TaxonomyFieldOption

	for i := range options {
		if options[i].SourceType == sourceType && options[i].FieldID == fieldID {
			noSource = &options[i]
		}
	}

	require.NotNil(t, noSource, "expected a discovered field option for the no-source bucket")
	require.Empty(t, noSource.SourceID, "no-source bucket must expose an empty source_id")
	require.Equal(t, 2, noSource.RecordCount, "NULL and blank source_id must collapse into one bucket")

	// Counting the empty-source scope matches both NULL and blank feedback rows.
	scope := models.TaxonomyScope{
		TenantID:   tenantID,
		SourceType: sourceType,
		SourceID:   "",
		FieldID:    fieldID,
	}

	recordCount, _, _, err := repo.CountScopeInput(ctx, scope, embeddingModel)
	require.NoError(t, err)
	require.Equal(t, 2, recordCount, "empty-source scope must null-safe match NULL/blank source rows")

	// A taxonomy run can be created for the empty-source scope and is found by the
	// in-progress guard (empty string is a valid, comparable key in taxonomy tables).
	run, created, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
		TaxonomyScope:  scope,
		RecordCount:    recordCount,
		EmbeddingCount: 0,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.Empty(t, run.SourceID)

	_, createdAgain, err := repo.CreateRunIfAvailable(ctx, repository.CreateTaxonomyRunParams{
		TaxonomyScope: scope,
	})
	require.NoError(t, err)
	require.False(t, createdAgain, "a second run for the same empty-source scope must reuse the in-progress run")
}
