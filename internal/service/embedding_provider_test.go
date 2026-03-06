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
	p := NewEmbeddingProvider(inserter, "sk-test", "model-name", "embeddings", 3, "", nil)

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
	assert.Equal(t, "model-name", inserter.insertCalls[0].args.Model)
	assert.NotEmpty(t, inserter.insertCalls[0].args.ValueTextHash, "dedupe key should include input hash")
	assert.NotNil(t, inserter.insertCalls[0].opts)
	assert.Equal(t, "embeddings", inserter.insertCalls[0].opts.Queue)
	assert.Equal(t, 3, inserter.insertCalls[0].opts.MaxAttempts)
}

func TestEmbeddingProvider_PublishEvent_FeedbackRecordCreated_dataIsValueNotPointer_skips(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "sk-test", "model-name", "embeddings", 3, "", nil)

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
	p := NewEmbeddingProvider(inserter, "sk-test", "model-name", "embeddings", 3, "", nil)

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
	p := NewEmbeddingProvider(inserter, "sk-test", "model-name", "embeddings", 3, "", nil)

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
	p := NewEmbeddingProvider(inserter, "sk-test", "model-name", "embeddings", 3, "", nil)

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

func TestEmbeddingPrefixForProvider(t *testing.T) {
	t.Run("openai returns empty", func(t *testing.T) { assert.Empty(t, EmbeddingPrefixForProvider("openai")) })
	t.Run("google returns empty", func(t *testing.T) { assert.Empty(t, EmbeddingPrefixForProvider("google")) })
	t.Run("google-vertex returns empty", func(t *testing.T) { assert.Empty(t, EmbeddingPrefixForProvider("google-vertex")) })
	t.Run("unknown returns empty", func(t *testing.T) { assert.Empty(t, EmbeddingPrefixForProvider("unknown")) })
}
