package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestEmbeddingProvider_PublishEvent_FeedbackRecordCreated_withValueText_enqueues(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "model-name", "embeddings", 3, "", nil)

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

	p.PublishEvent(context.Background(), event)

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

func TestEmbeddingProvider_PublishEvent_FeedbackRecordCreated_dataIsValueNotPointer_skips(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "model-name", "embeddings", 3, "", nil)

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

	p.PublishEvent(context.Background(), event)

	assert.Empty(t, inserter.insertCalls, "provider should skip when event.Data is value, not *FeedbackRecord")
}

func TestEmbeddingProvider_PublishEvent_FeedbackRecordCreated_emptyValueText_skips(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "model-name", "embeddings", 3, "", nil)

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

	p.PublishEvent(context.Background(), event)

	assert.Empty(t, inserter.insertCalls)
}

func TestEmbeddingProvider_PublishEvent_FeedbackRecordUpdated_valueTextInChangedFields_enqueues(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "model-name", "embeddings", 3, "", nil)

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

	p.PublishEvent(context.Background(), event)

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.Equal(t, "model-name", inserter.insertCalls[0].args.Model)
	assert.NotEmpty(t, inserter.insertCalls[0].args.ValueTextHash)
}

func TestEmbeddingProvider_PublishEvent_FeedbackRecordUpdated_fieldLabelInChangedFields_enqueues(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "model-name", "embeddings", 3, "", nil)

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

	p.PublishEvent(context.Background(), event)

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.NotEmpty(t, inserter.insertCalls[0].args.ValueTextHash)
}

func TestEmbeddingProvider_PublishEvent_TaxonomyTranslatedPrefersTranslatedText(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProviderForInputKind(
		inserter,
		"taxonomy:model:translated-v1",
		"embeddings",
		3,
		"",
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

	p.PublishEvent(context.Background(), event)

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.Equal(t, "taxonomy:model:translated-v1", inserter.insertCalls[0].args.Model)
	assert.Equal(t, models.EmbeddingInputKindTaxonomyTranslated, inserter.insertCalls[0].args.InputKind)
	assert.Equal(t, hashContent("Question: What went wrong?\nAnswer: The washing machine does not start"),
		inserter.insertCalls[0].args.ValueTextHash)
}

func TestBuildEmbeddingInput(t *testing.T) {
	t.Run("label and value produces Question/Answer format", func(t *testing.T) {
		out := BuildEmbeddingInput(
			new("What features are you missing?"),
			new("Dashboards, Charts"),
			"",
		)
		assert.Equal(t, "Question: What features are you missing?\nAnswer: Dashboards, Charts", out)
	})
	t.Run("empty label returns value only", func(t *testing.T) {
		out := BuildEmbeddingInput(nil, new("Just the value"), "")
		assert.Equal(t, "Just the value", out)
	})
	t.Run("nil valueText returns empty", func(t *testing.T) {
		assert.Empty(t, BuildEmbeddingInput(new("Label"), nil, ""))
	})
	t.Run("whitespace value returns empty", func(t *testing.T) {
		assert.Empty(t, BuildEmbeddingInput(nil, new("   "), ""))
	})
	t.Run("prefix is prepended", func(t *testing.T) {
		out := BuildEmbeddingInput(new("Q"), new("A"), "search_document: ")
		assert.Equal(t, "search_document: Question: Q\nAnswer: A", out)
	})
}

func TestBuildEmbeddingInputForKind(t *testing.T) {
	fieldLabel := "Open feedback"
	valueText := "Original text"
	translated := "Translated text"

	out := BuildEmbeddingInputFromValues(
		&fieldLabel,
		&valueText,
		&translated,
		models.EmbeddingInputKindTaxonomyTranslated,
		"",
	)
	assert.Equal(t, "Question: Open feedback\nAnswer: Translated text", out)

	blankTranslated := "  "
	out = BuildEmbeddingInputFromValues(
		&fieldLabel,
		&valueText,
		&blankTranslated,
		models.EmbeddingInputKindTaxonomyTranslated,
		"",
	)
	assert.Equal(t, "Question: Open feedback\nAnswer: Original text", out)
}

func TestTaxonomyEmbeddingModel(t *testing.T) {
	assert.Equal(t, "taxonomy:text-embedding:translated-v1", TaxonomyEmbeddingModel("text-embedding", ""))
	assert.Equal(t, "custom-taxonomy-model", TaxonomyEmbeddingModel("text-embedding", " custom-taxonomy-model "))
	assert.Empty(t, TaxonomyEmbeddingModel(" ", ""))
}
