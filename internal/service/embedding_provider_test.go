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

func ptrString(s string) *string {
	return &s
}

func TestEmbeddingProvider_PublishEvent_FeedbackRecordCreated_withValueText_enqueues(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "sk-test", "text-embedding-3-small", "embeddings", 3, nil)

	recordID := uuid.Must(uuid.NewV7())
	valueText := "Some feedback text"
	event := Event{
		ID:        uuid.Must(uuid.NewV7()),
		Type:      datatypes.FeedbackRecordCreated,
		Timestamp: time.Now(),
		Data: &models.FeedbackRecord{
			ID:        recordID,
			FieldType: models.FieldTypeText,
			ValueText: ptrString(valueText),
		},
	}

	p.PublishEvent(context.Background(), event)

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.Equal(t, "text-embedding-3-small", inserter.insertCalls[0].args.Model)
	assert.NotEmpty(t, inserter.insertCalls[0].args.ValueTextHash, "dedupe key should include input hash")
	assert.NotNil(t, inserter.insertCalls[0].opts)
	assert.Equal(t, "embeddings", inserter.insertCalls[0].opts.Queue)
	assert.Equal(t, 3, inserter.insertCalls[0].opts.MaxAttempts)
}

func TestEmbeddingProvider_PublishEvent_FeedbackRecordCreated_dataIsValueNotPointer_skips(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "sk-test", "text-embedding-3-small", "embeddings", 3, nil)

	// Pass value type (as was happening before the service fix) — provider should skip and not enqueue.
	event := Event{
		ID:        uuid.Must(uuid.NewV7()),
		Type:      datatypes.FeedbackRecordCreated,
		Timestamp: time.Now(),
		Data: models.FeedbackRecord{
			ID:        uuid.Must(uuid.NewV7()),
			FieldType: models.FieldTypeText,
			ValueText: ptrString("hello"),
		},
	}

	p.PublishEvent(context.Background(), event)

	assert.Empty(t, inserter.insertCalls, "provider should skip when event.Data is value, not *FeedbackRecord")
}

func TestEmbeddingProvider_PublishEvent_FeedbackRecordCreated_emptyValueText_skips(t *testing.T) {
	inserter := &mockEmbeddingInserter{}
	p := NewEmbeddingProvider(inserter, "sk-test", "text-embedding-3-small", "embeddings", 3, nil)

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
	p := NewEmbeddingProvider(inserter, "sk-test", "text-embedding-3-small", "embeddings", 3, nil)

	recordID := uuid.Must(uuid.NewV7())
	event := Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          datatypes.FeedbackRecordUpdated,
		Timestamp:     time.Now(),
		ChangedFields: []string{"value_text"},
		Data: &models.FeedbackRecord{
			ID:        recordID,
			FieldType: models.FieldTypeText,
			ValueText: ptrString("updated text"),
		},
	}

	p.PublishEvent(context.Background(), event)

	require.Len(t, inserter.insertCalls, 1)
	assert.Equal(t, recordID, inserter.insertCalls[0].args.FeedbackRecordID)
	assert.Equal(t, "text-embedding-3-small", inserter.insertCalls[0].args.Model)
	assert.NotEmpty(t, inserter.insertCalls[0].args.ValueTextHash)
}
