package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type mockWebhookInserter struct {
	insertCalls []insertCall
	insertErr   error
}

type insertCall struct {
	args WebhookDispatchArgs
	opts *river.InsertOpts
}

func (m *mockWebhookInserter) Insert(_ context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if m.insertErr != nil {
		return nil, m.insertErr
	}
	a, ok := args.(WebhookDispatchArgs)
	if !ok {
		return nil, errors.New("wrong args type")
	}
	m.insertCalls = append(m.insertCalls, insertCall{args: a, opts: opts})
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: 1}}, nil
}

// mockProviderRepo implements only ListEnabledForEventType for provider tests.
type mockProviderRepo struct {
	webhooks []models.Webhook
	err      error
}

func (m *mockProviderRepo) ListEnabledForEventType(_ context.Context, _ string) ([]models.Webhook, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.webhooks, nil
}

// Stub other WebhooksRepository methods so mockProviderRepo can be used as WebhooksRepository.
func (m *mockProviderRepo) Create(_ context.Context, _ *models.CreateWebhookRequest) (*models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProviderRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProviderRepo) List(_ context.Context, _ *models.ListWebhooksFilters) ([]models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProviderRepo) Count(_ context.Context, _ *models.ListWebhooksFilters) (int64, error) {
	return 0, errors.New("not implemented")
}

func (m *mockProviderRepo) Update(_ context.Context, _ uuid.UUID, _ *models.UpdateWebhookRequest) (*models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProviderRepo) Delete(_ context.Context, _ uuid.UUID) error {
	return errors.New("not implemented")
}

func (m *mockProviderRepo) ListEnabled(_ context.Context) ([]models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func TestWebhookProvider_PublishEvent(t *testing.T) {
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7())
	eventType := datatypes.FeedbackRecordCreated
	wh1 := uuid.Must(uuid.NewV7())
	wh2 := uuid.Must(uuid.NewV7())

	t.Run("inserts one job per webhook with correct opts", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{
			webhooks: []models.Webhook{{ID: wh1}, {ID: wh2}},
		}
		provider := NewWebhookProvider(inserter, repo, 3)

		event := Event{
			ID:        eventID,
			Type:      eventType,
			Timestamp: time.Now().Unix(),
			Data:      map[string]string{"id": "123"},
		}

		provider.PublishEvent(ctx, event)

		if n := len(inserter.insertCalls); n != 2 {
			t.Fatalf("Insert called %d times, want 2", n)
		}
		for i, call := range inserter.insertCalls {
			if call.args.EventID != eventID {
				t.Errorf("call %d EventID = %v, want %v", i, call.args.EventID, eventID)
			}
			if call.args.EventType != eventType.String() {
				t.Errorf("call %d EventType = %q, want %q", i, call.args.EventType, eventType.String())
			}
			wantID := repo.webhooks[i].ID
			if call.args.WebhookID != wantID {
				t.Errorf("call %d WebhookID = %v, want %v", i, call.args.WebhookID, wantID)
			}
			if call.opts == nil || call.opts.MaxAttempts != 3 {
				t.Errorf("call %d MaxAttempts = %v, want 3", i, call.opts)
			}
			if call.opts != nil && (!call.opts.UniqueOpts.ByArgs || call.opts.UniqueOpts.ByPeriod != 24*time.Hour) {
				t.Errorf("call %d UniqueOpts = %+v", i, call.opts.UniqueOpts)
			}
		}
	})

	t.Run("no insert when list returns empty", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{webhooks: nil}
		provider := NewWebhookProvider(inserter, repo, 3)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now().Unix(), Data: nil}
		provider.PublishEvent(ctx, event)
		if len(inserter.insertCalls) != 0 {
			t.Errorf("Insert called %d times, want 0", len(inserter.insertCalls))
		}
	})

	t.Run("no insert when list returns error", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{err: errors.New("db error")}
		provider := NewWebhookProvider(inserter, repo, 3)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now().Unix(), Data: nil}
		provider.PublishEvent(ctx, event)
		if len(inserter.insertCalls) != 0 {
			t.Errorf("Insert called %d times, want 0", len(inserter.insertCalls))
		}
	})

	t.Run("continues with remaining webhooks when one insert fails", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{webhooks: []models.Webhook{{ID: wh1}, {ID: wh2}}}
		provider := NewWebhookProvider(inserter, repo, 5)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now().Unix(), Data: nil}
		provider.PublishEvent(ctx, event)
		if len(inserter.insertCalls) != 2 {
			t.Errorf("Insert called %d times, want 2", len(inserter.insertCalls))
		}
		if inserter.insertCalls[0].opts.MaxAttempts != 5 {
			t.Errorf("MaxAttempts = %d, want 5", inserter.insertCalls[0].opts.MaxAttempts)
		}
	})
}
