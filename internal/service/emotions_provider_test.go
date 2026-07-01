package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

type mockEmotionsInserter struct {
	calls     []emotionsInsertCall
	insertErr error
}

type emotionsInsertCall struct {
	args FeedbackEmotionsArgs
	opts *river.InsertOpts
}

func (m *mockEmotionsInserter) Insert(
	_ context.Context, args river.JobArgs, opts *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	emotionsArgs, _ := args.(FeedbackEmotionsArgs)
	m.calls = append(m.calls, emotionsInsertCall{args: emotionsArgs, opts: opts})

	if m.insertErr != nil {
		return nil, m.insertErr
	}

	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: 1}}, nil
}

func emotionsTextRecord(id uuid.UUID, valueText *string) *models.FeedbackRecord {
	return &models.FeedbackRecord{ID: id, FieldType: models.FieldTypeText, ValueText: valueText}
}

func TestEmotionsProvider_PublishEvent_Created_withText_enqueues(t *testing.T) {
	inserter := &mockEmotionsInserter{}
	provider := NewEmotionsProvider(inserter, stubSettingsResolver{}, EmotionsQueueName, 3, nil)

	recordID := uuid.Must(uuid.NewV7())
	text := "I am thrilled and a little scared"
	provider.PublishEvent(context.Background(), Event{
		ID:   uuid.Must(uuid.NewV7()),
		Type: datatypes.FeedbackRecordCreated,
		Data: emotionsTextRecord(recordID, &text),
	})

	require.Len(t, inserter.calls, 1)
	assert.Equal(t, recordID, inserter.calls[0].args.FeedbackRecordID)
	assert.NotEmpty(t, inserter.calls[0].args.ValueTextHash)
	assert.NotEqual(t, "empty", inserter.calls[0].args.ValueTextHash)
	assert.Equal(t, EmotionsQueueName, inserter.calls[0].opts.Queue)
	assert.Equal(t, 3, inserter.calls[0].opts.MaxAttempts)
	// Dedupe contract: identical (record, value_text) within the window is one job.
	assert.True(t, inserter.calls[0].opts.UniqueOpts.ByArgs, "dedupe by args")
	assert.Equal(t, uniqueByPeriodEmotions, inserter.calls[0].opts.UniqueOpts.ByPeriod)
}

func TestEmotionsProvider_PublishEvent_Created_skips(t *testing.T) {
	emptyText := "  "

	tests := map[string]struct {
		data any
	}{
		"nil value_text":      {data: emotionsTextRecord(uuid.Must(uuid.NewV7()), nil)},
		"whitespace value":    {data: emotionsTextRecord(uuid.Must(uuid.NewV7()), &emptyText)},
		"not a text field":    {data: &models.FeedbackRecord{ID: uuid.Must(uuid.NewV7()), FieldType: models.FieldTypeNumber}},
		"data is not pointer": {data: models.FeedbackRecord{ID: uuid.Must(uuid.NewV7()), FieldType: models.FieldTypeText}},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			inserter := &mockEmotionsInserter{}
			provider := NewEmotionsProvider(inserter, stubSettingsResolver{}, EmotionsQueueName, 3, nil)

			provider.PublishEvent(context.Background(), Event{
				ID:   uuid.Must(uuid.NewV7()),
				Type: datatypes.FeedbackRecordCreated,
				Data: testCase.data,
			})

			assert.Empty(t, inserter.calls)
		})
	}
}

// Like sentiment, emotions depend only on value_text — a source-language change alone must not
// re-enqueue.
func TestEmotionsProvider_PublishEvent_Updated_onlyLanguageChanged_skips(t *testing.T) {
	inserter := &mockEmotionsInserter{}
	provider := NewEmotionsProvider(inserter, stubSettingsResolver{}, EmotionsQueueName, 3, nil)

	text := "unchanged text"
	provider.PublishEvent(context.Background(), Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          datatypes.FeedbackRecordUpdated,
		ChangedFields: []string{"language"},
		Data:          emotionsTextRecord(uuid.Must(uuid.NewV7()), &text),
	})

	assert.Empty(t, inserter.calls, "a language-only change does not affect emotions")
}

// On update the job is enqueued even when value_text is now empty, so the worker can clear stale
// emotions.
func TestEmotionsProvider_PublishEvent_Updated_valueTextNowEmpty_enqueues(t *testing.T) {
	inserter := &mockEmotionsInserter{}
	provider := NewEmotionsProvider(inserter, stubSettingsResolver{}, EmotionsQueueName, 3, nil)

	provider.PublishEvent(context.Background(), Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          datatypes.FeedbackRecordUpdated,
		ChangedFields: []string{"value_text"},
		Data:          emotionsTextRecord(uuid.Must(uuid.NewV7()), nil),
	})

	require.Len(t, inserter.calls, 1)
	assert.Equal(t, "empty", inserter.calls[0].args.ValueTextHash, "a cleared value hashes to the empty marker")
}

func TestEmotionsContentHash(t *testing.T) {
	whitespace := "   "
	hello := "hello"
	helloPadded := "  hello  "
	world := "world"

	assert.Equal(t, "empty", emotionsContentHash(nil))
	assert.Equal(t, "empty", emotionsContentHash(&whitespace))

	assert.Equal(t, emotionsContentHash(&hello), emotionsContentHash(&helloPadded),
		"the hash is over trimmed, normalized text")
	assert.NotEqual(t, emotionsContentHash(&hello), emotionsContentHash(&world))
	assert.NotEqual(t, "empty", emotionsContentHash(&hello))
}

func TestEmotionsProvider_PublishEvent_skipsWhenDisabledForTenant(t *testing.T) {
	inserter := &mockEmotionsInserter{}
	disabled := false
	provider := NewEmotionsProvider(
		inserter, stubSettingsResolver{settings: models.EnrichmentSettings{EmotionsEnabled: &disabled}},
		EmotionsQueueName, 3, nil)

	text := "I am thrilled"
	provider.PublishEvent(context.Background(), Event{
		ID:   uuid.Must(uuid.NewV7()),
		Type: datatypes.FeedbackRecordCreated,
		Data: emotionsTextRecord(uuid.Must(uuid.NewV7()), &text),
	})

	assert.Empty(t, inserter.calls, "a tenant that switched emotions off is not enqueued")
}

func TestEmotionsProvider_PublishEvent_skipsOnSettingsReadError(t *testing.T) {
	inserter := &mockEmotionsInserter{}
	provider := NewEmotionsProvider(
		inserter, stubSettingsResolver{err: errors.New("db down")}, EmotionsQueueName, 3, nil)

	text := "I am thrilled"
	provider.PublishEvent(context.Background(), Event{
		ID:   uuid.Must(uuid.NewV7()),
		Type: datatypes.FeedbackRecordCreated,
		Data: emotionsTextRecord(uuid.Must(uuid.NewV7()), &text),
	})

	assert.Empty(t, inserter.calls, "a settings read failure skips rather than enqueuing blindly")
}
