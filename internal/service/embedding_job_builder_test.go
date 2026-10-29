package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

type mockEmbeddingInserter struct {
	insertCalls []insertCall
	insertErr   error
}

type insertCall struct {
	args FeedbackEmbeddingArgs
	opts *river.InsertOpts
}

func (m *mockEmbeddingInserter) Insert(
	_ context.Context,
	args river.JobArgs,
	opts *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	embeddingArgs, ok := args.(FeedbackEmbeddingArgs)
	if !ok {
		m.insertCalls = append(m.insertCalls, insertCall{opts: opts})

		return nil, m.insertErr
	}

	m.insertCalls = append(m.insertCalls, insertCall{args: embeddingArgs, opts: opts})
	if m.insertErr != nil {
		return nil, m.insertErr
	}

	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: 1}}, nil
}

// InsertManyTx records each job as an insert call, so the enqueuer's tests share the
// single-insert assertions.
func (m *mockEmbeddingInserter) InsertManyTx(
	ctx context.Context, _ pgx.Tx, params []river.InsertManyParams,
) ([]*rivertype.JobInsertResult, error) {
	results := make([]*rivertype.JobInsertResult, 0, len(params))

	for _, param := range params {
		result, err := m.Insert(ctx, param.Args, param.InsertOpts)
		if err != nil {
			return nil, err
		}

		results = append(results, result)
	}

	return results, nil
}

func TestEmbeddingEnqueuer_EnqueueTx_FeedbackRecordCreated_withValueText_enqueues(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingJobBuilder("model-name", "embeddings", 3, "", 0, nil)

	recordID := uuid.Must(uuid.NewV7())
	valueText := "Some feedback text"
	event := Event{
		ID:        uuid.Must(uuid.NewV7()),
		Type:      datatypes.FeedbackRecordCreated,
		Timestamp: time.Now(),
		Data: &models.FeedbackRecord{
			ID:        recordID,
			FieldType: models.FieldTypeText,
			ValueText: new(valueText),
		},
	}

	require.NoError(t, NewEmbeddingEnqueuer(inserter, p).EnqueueTx(context.Background(), nil, event))

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.Equal(t, event.ID, inserter.insertCalls[0].args.EventID, "event id is carried into the job args")
	assert.Equal(t, "model-name", inserter.insertCalls[0].args.Model)
	assert.Equal(t, models.EmbeddingInputKindRaw, inserter.insertCalls[0].args.InputKind)
	assert.NotEmpty(t, inserter.insertCalls[0].args.ValueTextHash, "dedupe key should include input hash")
	assert.NotNil(t, inserter.insertCalls[0].opts)
	assert.Equal(t, "embeddings", inserter.insertCalls[0].opts.Queue)
	assert.Equal(t, 3, inserter.insertCalls[0].opts.MaxAttempts)
}

func TestEmbeddingEnqueuer_EnqueueTx_FeedbackRecordCreated_dataIsValueNotPointer_skips(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingJobBuilder("model-name", "embeddings", 3, "", 0, nil)

	// Pass value type (as was happening before the service fix) — provider should skip and not enqueue.
	event := Event{
		ID:        uuid.Must(uuid.NewV7()),
		Type:      datatypes.FeedbackRecordCreated,
		Timestamp: time.Now(),
		Data: models.FeedbackRecord{
			ID:        uuid.Must(uuid.NewV7()),
			FieldType: models.FieldTypeText,
			ValueText: new("hello"),
		},
	}

	require.NoError(t, NewEmbeddingEnqueuer(inserter, p).EnqueueTx(context.Background(), nil, event))

	assert.Empty(t, inserter.insertCalls, "provider should skip when event.Data is value, not *FeedbackRecord")
}

func TestEmbeddingEnqueuer_EnqueueTx_FeedbackRecordCreated_emptyValueText_skips(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingJobBuilder("model-name", "embeddings", 3, "", 0, nil)

	event := Event{
		ID:        uuid.Must(uuid.NewV7()),
		Type:      datatypes.FeedbackRecordCreated,
		Timestamp: time.Now(),
		Data: &models.FeedbackRecord{
			ID:        uuid.Must(uuid.NewV7()),
			FieldType: models.FieldTypeText,
			ValueText: nil,
		},
	}

	require.NoError(t, NewEmbeddingEnqueuer(inserter, p).EnqueueTx(context.Background(), nil, event))

	assert.Empty(t, inserter.insertCalls)
}

func TestEmbeddingEnqueuer_EnqueueTx_FeedbackRecordUpdated_valueTextInChangedFields_enqueues(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingJobBuilder("model-name", "embeddings", 3, "", 0, nil)

	recordID := uuid.Must(uuid.NewV7())
	event := Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          datatypes.FeedbackRecordUpdated,
		Timestamp:     time.Now(),
		ChangedFields: []string{"value_text"},
		Data: &models.FeedbackRecord{
			ID:        recordID,
			FieldType: models.FieldTypeText,
			ValueText: new("updated text"),
		},
	}

	require.NoError(t, NewEmbeddingEnqueuer(inserter, p).EnqueueTx(context.Background(), nil, event))

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.Equal(t, "model-name", inserter.insertCalls[0].args.Model)
	assert.NotEmpty(t, inserter.insertCalls[0].args.ValueTextHash)
}

func TestEmbeddingEnqueuer_EnqueueTx_FeedbackRecordUpdated_fieldLabelInChangedFields_enqueues(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingJobBuilder("model-name", "embeddings", 3, "", 0, nil)

	recordID := uuid.Must(uuid.NewV7())
	event := Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          datatypes.FeedbackRecordUpdated,
		Timestamp:     time.Now(),
		ChangedFields: []string{"field_label"},
		Data: &models.FeedbackRecord{
			ID:         recordID,
			FieldType:  models.FieldTypeText,
			FieldLabel: new("Updated label"),
			ValueText:  new("same value"),
		},
	}

	require.NoError(t, NewEmbeddingEnqueuer(inserter, p).EnqueueTx(context.Background(), nil, event))

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.NotEmpty(t, inserter.insertCalls[0].args.ValueTextHash)
}

func TestEmbeddingEnqueuer_EnqueueTx_TaxonomyTranslatedPrefersTranslatedText(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingJobBuilderForInputKind(
		"taxonomy:model:translated-v1",
		"embeddings",
		3,
		"",
		0,
		nil,
		models.EmbeddingInputKindTaxonomyTranslated,
	)

	recordID := uuid.Must(uuid.NewV7())
	fieldLabel := "What went wrong?"
	valueText := "La machine ne demarre pas"
	translated := "The washing machine does not start"
	event := Event{
		ID:        uuid.Must(uuid.NewV7()),
		Type:      datatypes.FeedbackRecordCreated,
		Timestamp: time.Now(),
		Data: &models.FeedbackRecord{
			ID:                  recordID,
			FieldType:           models.FieldTypeText,
			FieldLabel:          &fieldLabel,
			ValueText:           &valueText,
			ValueTextTranslated: &translated,
		},
	}

	require.NoError(t, NewEmbeddingEnqueuer(inserter, p).EnqueueTx(context.Background(), nil, event))

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.Equal(t, "taxonomy:model:translated-v1", inserter.insertCalls[0].args.Model)
	assert.Equal(t, models.EmbeddingInputKindTaxonomyTranslated, inserter.insertCalls[0].args.InputKind)
	assert.Equal(t, hashContent("Question: What went wrong?\nAnswer: The washing machine does not start"),
		inserter.insertCalls[0].args.ValueTextHash)
}

func TestBuildEmbeddingInput(t *testing.T) {
	t.Run("label and value produces Question/Answer format", func(t *testing.T) {
		out, trimmed := BuildEmbeddingInput(
			new("What features are you missing?"),
			new("Dashboards, Charts"),
			"",
			TextCap{},
		)
		assert.Equal(t, "Question: What features are you missing?\nAnswer: Dashboards, Charts", out)
		assert.False(t, trimmed)
	})
	t.Run("empty label returns value only", func(t *testing.T) {
		out, _ := BuildEmbeddingInput(nil, new("Just the value"), "", TextCap{})
		assert.Equal(t, "Just the value", out)
	})
	t.Run("nil valueText returns empty", func(t *testing.T) {
		out, _ := BuildEmbeddingInput(new("Label"), nil, "", TextCap{})
		assert.Empty(t, out)
	})
	t.Run("whitespace value returns empty", func(t *testing.T) {
		out, _ := BuildEmbeddingInput(nil, new("   "), "", TextCap{})
		assert.Empty(t, out)
	})
	t.Run("prefix is prepended", func(t *testing.T) {
		out, _ := BuildEmbeddingInput(new("Q"), new("A"), "search_document: ", TextCap{})
		assert.Equal(t, "search_document: Question: Q\nAnswer: A", out)
	})
	t.Run("input within the cap is not cut", func(t *testing.T) {
		// The label is part of what the provider sees, so it counts against the cap.
		label, value := "Q", "Answer"

		out, trimmed := BuildEmbeddingInput(&label, &value, "", TextCap{MaxBytes: len("Question: Q\nAnswer: Answer")})

		assert.Equal(t, "Question: Q\nAnswer: Answer", out)
		assert.False(t, trimmed)
	})
	t.Run("input over the cap keeps the head and reports the cut", func(t *testing.T) {
		label, value := "Q", "abcdefghij"

		out, trimmed := BuildEmbeddingInput(&label, &value, "", TextCap{MaxBytes: len("Question: Q\nAnswer: abcd")})

		assert.Equal(t, "Question: Q\nAnswer: abcd", out)
		assert.True(t, trimmed)
	})
	t.Run("a cut never splits a rune", func(t *testing.T) {
		value := "ééé" // 2 bytes each

		out, trimmed := BuildEmbeddingInput(nil, &value, "", TextCap{MaxBytes: 3})

		assert.Equal(t, "é", out, "cut lands back on the 2-byte boundary, not mid-rune")
		assert.True(t, trimmed)
	})
}

func TestBuildEmbeddingInputForKind(t *testing.T) {
	fieldLabel := "Open feedback"
	valueText := "Original text"
	translated := "Translated text"

	out, _ := BuildEmbeddingInputFromValues(
		&fieldLabel,
		&valueText,
		&translated,
		models.EmbeddingInputKindTaxonomyTranslated,
		"",
		TextCap{},
	)
	assert.Equal(t, "Question: Open feedback\nAnswer: Translated text", out)

	blankTranslated := "  "
	out, _ = BuildEmbeddingInputFromValues(
		&fieldLabel,
		&valueText,
		&blankTranslated,
		models.EmbeddingInputKindTaxonomyTranslated,
		"",
		TextCap{},
	)
	assert.Equal(t, "Question: Open feedback\nAnswer: Original text", out)
}

func TestTaxonomyEmbeddingModel(t *testing.T) {
	assert.Equal(t, "taxonomy:text-embedding:translated-v1", TaxonomyEmbeddingModel("text-embedding", ""))
	assert.Equal(t, "custom-taxonomy-model", TaxonomyEmbeddingModel("text-embedding", " custom-taxonomy-model "))
	assert.Empty(t, TaxonomyEmbeddingModel(" ", ""))
}
