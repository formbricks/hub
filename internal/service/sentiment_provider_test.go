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

type mockSentimentInserter struct {
	calls     []sentimentInsertCall
	insertErr error
}

type sentimentInsertCall struct {
	args FeedbackSentimentArgs
	opts *river.InsertOpts
}

func (m *mockSentimentInserter) Insert(
	_ context.Context, args river.JobArgs, opts *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	sentimentArgs, _ := args.(FeedbackSentimentArgs)
	m.calls = append(m.calls, sentimentInsertCall{args: sentimentArgs, opts: opts})

	if m.insertErr != nil {
		return nil, m.insertErr
	}

	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: 1}}, nil
}

func sentimentTextRecord(id uuid.UUID, valueText *string) *models.FeedbackRecord {
	return &models.FeedbackRecord{ID: id, FieldType: models.FieldTypeText, ValueText: valueText}
}

func TestSentimentProvider_PublishEvent_Created_withText_enqueues(t *testing.T) {
	inserter := &mockSentimentInserter{}
	provider := NewSentimentProvider(inserter, stubSettingsResolver{}, SentimentsQueueName, 3, nil)

	recordID := uuid.Must(uuid.NewV7())
	text := "Great product"
	provider.PublishEvent(context.Background(), Event{
		ID:   uuid.Must(uuid.NewV7()),
		Type: datatypes.FeedbackRecordCreated,
		Data: sentimentTextRecord(recordID, &text),
	})

	require.Len(t, inserter.calls, 1)
	assert.Equal(t, recordID, inserter.calls[0].args.FeedbackRecordID)
	assert.NotEmpty(t, inserter.calls[0].args.ValueTextHash)
	assert.NotEqual(t, "empty", inserter.calls[0].args.ValueTextHash)
	assert.Equal(t, SentimentsQueueName, inserter.calls[0].opts.Queue)
	assert.Equal(t, 3, inserter.calls[0].opts.MaxAttempts)
}

func TestSentimentProvider_PublishEvent_Created_skips(t *testing.T) {
	emptyText := "  "

	tests := map[string]struct {
		data any
	}{
		"nil value_text":      {data: sentimentTextRecord(uuid.Must(uuid.NewV7()), nil)},
		"whitespace value":    {data: sentimentTextRecord(uuid.Must(uuid.NewV7()), &emptyText)},
		"not a text field":    {data: &models.FeedbackRecord{ID: uuid.Must(uuid.NewV7()), FieldType: models.FieldTypeNumber}},
		"data is not pointer": {data: models.FeedbackRecord{ID: uuid.Must(uuid.NewV7()), FieldType: models.FieldTypeText}},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			inserter := &mockSentimentInserter{}
			provider := NewSentimentProvider(inserter, stubSettingsResolver{}, SentimentsQueueName, 3, nil)

			provider.PublishEvent(context.Background(), Event{
				ID:   uuid.Must(uuid.NewV7()),
				Type: datatypes.FeedbackRecordCreated,
				Data: testCase.data,
			})

			assert.Empty(t, inserter.calls)
		})
	}
}

func TestSentimentProvider_PublishEvent_Updated_valueTextChanged_enqueues(t *testing.T) {
	inserter := &mockSentimentInserter{}
	provider := NewSentimentProvider(inserter, stubSettingsResolver{}, SentimentsQueueName, 3, nil)

	recordID := uuid.Must(uuid.NewV7())
	text := "updated text"
	provider.PublishEvent(context.Background(), Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          datatypes.FeedbackRecordUpdated,
		ChangedFields: []string{"value_text"},
		Data:          sentimentTextRecord(recordID, &text),
	})

	require.Len(t, inserter.calls, 1)
	assert.Equal(t, recordID, inserter.calls[0].args.FeedbackRecordID)
}

// Unlike translation, sentiment depends only on value_text — a source-language change alone
// must not re-enqueue.
func TestSentimentProvider_PublishEvent_Updated_onlyLanguageChanged_skips(t *testing.T) {
	inserter := &mockSentimentInserter{}
	provider := NewSentimentProvider(inserter, stubSettingsResolver{}, SentimentsQueueName, 3, nil)

	text := "unchanged text"
	provider.PublishEvent(context.Background(), Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          datatypes.FeedbackRecordUpdated,
		ChangedFields: []string{"language"},
		Data:          sentimentTextRecord(uuid.Must(uuid.NewV7()), &text),
	})

	assert.Empty(t, inserter.calls, "a language-only change does not affect sentiment")
}

// On update the job is enqueued even when value_text is now empty, so the worker can clear a
// stale sentiment.
func TestSentimentProvider_PublishEvent_Updated_valueTextNowEmpty_enqueues(t *testing.T) {
	inserter := &mockSentimentInserter{}
	provider := NewSentimentProvider(inserter, stubSettingsResolver{}, SentimentsQueueName, 3, nil)

	provider.PublishEvent(context.Background(), Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          datatypes.FeedbackRecordUpdated,
		ChangedFields: []string{"value_text"},
		Data:          sentimentTextRecord(uuid.Must(uuid.NewV7()), nil),
	})

	require.Len(t, inserter.calls, 1)
	assert.Equal(t, "empty", inserter.calls[0].args.ValueTextHash, "a cleared value hashes to the empty marker")
}

func TestSentimentContentHash(t *testing.T) {
	whitespace := "   "
	hello := "hello"
	helloPadded := "  hello  "
	world := "world"

	assert.Equal(t, "empty", sentimentContentHash(nil))
	assert.Equal(t, "empty", sentimentContentHash(&whitespace))

	assert.Equal(t, sentimentContentHash(&hello), sentimentContentHash(&helloPadded),
		"the hash is over trimmed, normalized text")
	assert.NotEqual(t, sentimentContentHash(&hello), sentimentContentHash(&world))
	assert.NotEqual(t, "empty", sentimentContentHash(&hello))
}

// stubSettingsResolver is a TenantSettingsReader returning fixed settings (or an error).
type stubSettingsResolver struct {
	settings models.EnrichmentSettings
	err      error
}

func (s stubSettingsResolver) GetSettings(_ context.Context, tenantID string) (*models.TenantSettings, error) {
	if s.err != nil {
		return nil, s.err
	}

	return &models.TenantSettings{TenantID: tenantID, Settings: s.settings}, nil
}

func TestSentimentProvider_PublishEvent_skipsWhenDisabledForTenant(t *testing.T) {
	inserter := &mockSentimentInserter{}
	disabled := false
	provider := NewSentimentProvider(
		inserter, stubSettingsResolver{settings: models.EnrichmentSettings{SentimentEnabled: &disabled}},
		SentimentsQueueName, 3, nil)

	text := "Great product"
	provider.PublishEvent(context.Background(), Event{
		ID:   uuid.Must(uuid.NewV7()),
		Type: datatypes.FeedbackRecordCreated,
		Data: sentimentTextRecord(uuid.Must(uuid.NewV7()), &text),
	})

	assert.Empty(t, inserter.calls, "a tenant that switched sentiment off is not enqueued")
}

func TestSentimentProvider_PublishEvent_enqueuesWhenExplicitlyEnabled(t *testing.T) {
	inserter := &mockSentimentInserter{}
	enabled := true
	provider := NewSentimentProvider(
		inserter, stubSettingsResolver{settings: models.EnrichmentSettings{SentimentEnabled: &enabled}},
		SentimentsQueueName, 3, nil)

	text := "Great product"
	provider.PublishEvent(context.Background(), Event{
		ID:   uuid.Must(uuid.NewV7()),
		Type: datatypes.FeedbackRecordCreated,
		Data: sentimentTextRecord(uuid.Must(uuid.NewV7()), &text),
	})

	require.Len(t, inserter.calls, 1)
}

func TestSentimentProvider_PublishEvent_skipsOnSettingsReadError(t *testing.T) {
	inserter := &mockSentimentInserter{}
	provider := NewSentimentProvider(
		inserter, stubSettingsResolver{err: errors.New("db down")}, SentimentsQueueName, 3, nil)

	text := "Great product"
	provider.PublishEvent(context.Background(), Event{
		ID:   uuid.Must(uuid.NewV7()),
		Type: datatypes.FeedbackRecordCreated,
		Data: sentimentTextRecord(uuid.Must(uuid.NewV7()), &text),
	})

	assert.Empty(t, inserter.calls, "a settings read failure skips rather than enqueuing blindly")
}
