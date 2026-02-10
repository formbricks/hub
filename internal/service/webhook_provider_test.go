package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
)

type mockWebhookInserter struct {
	insertManyCalls [][]river.InsertManyParams
	insertManyErr   error
}

func (m *mockWebhookInserter) InsertMany(_ context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	// Record a copy of params for assertions (even when returning error).
	cp := make([]river.InsertManyParams, len(params))
	copy(cp, params)
	m.insertManyCalls = append(m.insertManyCalls, cp)
	if m.insertManyErr != nil {
		return nil, m.insertManyErr
	}
	results := make([]*rivertype.JobInsertResult, len(params))
	for i := range results {
		results[i] = &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: int64(i + 1)}}
	}
	return results, nil
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

	t.Run("inserts one job per webhook via InsertMany with correct opts", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{
			webhooks: []models.Webhook{{ID: wh1}, {ID: wh2}},
		}
		provider := NewWebhookProvider(inserter, repo, 3, 500, nil)

		event := Event{
			ID:        eventID,
			Type:      eventType,
			Timestamp: time.Now(),
			Data:      map[string]string{"id": "123"},
		}

		provider.PublishEvent(ctx, event)

		if n := len(inserter.insertManyCalls); n != 1 {
			t.Fatalf("InsertMany called %d times, want 1", n)
		}
		params := inserter.insertManyCalls[0]
		if len(params) != 2 {
			t.Fatalf("InsertMany params length = %d, want 2", len(params))
		}
		for i, p := range params {
			args, ok := p.Args.(WebhookDispatchArgs)
			if !ok {
				t.Fatalf("param %d Args type = %T, want WebhookDispatchArgs", i, p.Args)
			}
			if args.EventID != eventID {
				t.Errorf("param %d EventID = %v, want %v", i, args.EventID, eventID)
			}
			if args.EventType != eventType.String() {
				t.Errorf("param %d EventType = %q, want %q", i, args.EventType, eventType.String())
			}
			wantID := repo.webhooks[i].ID
			if args.WebhookID != wantID {
				t.Errorf("param %d WebhookID = %v, want %v", i, args.WebhookID, wantID)
			}
			if p.InsertOpts == nil || p.InsertOpts.MaxAttempts != 3 {
				t.Errorf("param %d MaxAttempts = %v, want 3", i, p.InsertOpts)
			}
			if p.InsertOpts != nil && (!p.InsertOpts.UniqueOpts.ByArgs || p.InsertOpts.UniqueOpts.ByPeriod != 24*time.Hour) {
				t.Errorf("param %d UniqueOpts = %+v", i, p.InsertOpts.UniqueOpts)
			}
		}
	})

	t.Run("no InsertMany when list returns empty", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{webhooks: nil}
		provider := NewWebhookProvider(inserter, repo, 3, 500, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: nil}
		provider.PublishEvent(ctx, event)
		if len(inserter.insertManyCalls) != 0 {
			t.Errorf("InsertMany called %d times, want 0", len(inserter.insertManyCalls))
		}
	})

	t.Run("no InsertMany when list returns error", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{err: errors.New("db error")}
		provider := NewWebhookProvider(inserter, repo, 3, 500, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: nil}
		provider.PublishEvent(ctx, event)
		if len(inserter.insertManyCalls) != 0 {
			t.Errorf("InsertMany called %d times, want 0", len(inserter.insertManyCalls))
		}
	})

	t.Run("when InsertMany returns error, provider logs and returns", func(t *testing.T) {
		inserter := &mockWebhookInserter{insertManyErr: errors.New("river error")}
		repo := &mockProviderRepo{webhooks: []models.Webhook{{ID: wh1}, {ID: wh2}}}
		provider := NewWebhookProvider(inserter, repo, 5, 500, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: nil}
		provider.PublishEvent(ctx, event)
		// InsertMany was still called once (batch fails as a whole).
		if len(inserter.insertManyCalls) != 1 {
			t.Errorf("InsertMany called %d times, want 1", len(inserter.insertManyCalls))
		}
		if len(inserter.insertManyCalls[0]) != 2 {
			t.Errorf("InsertMany params length = %d, want 2", len(inserter.insertManyCalls[0]))
		}
		if inserter.insertManyCalls[0][0].InsertOpts.MaxAttempts != 5 {
			t.Errorf("MaxAttempts = %d, want 5", inserter.insertManyCalls[0][0].InsertOpts.MaxAttempts)
		}
	})

	t.Run("enqueues all webhooks in batches of maxFanOut", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		webhooks := make([]models.Webhook, 501)
		for i := range webhooks {
			webhooks[i] = models.Webhook{ID: uuid.Must(uuid.NewV7())}
		}
		repo := &mockProviderRepo{webhooks: webhooks}
		provider := NewWebhookProvider(inserter, repo, 3, 500, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: nil}
		provider.PublishEvent(ctx, event)
		if len(inserter.insertManyCalls) != 2 {
			t.Fatalf("InsertMany called %d times, want 2 (batches of 500 and 1)", len(inserter.insertManyCalls))
		}
		if len(inserter.insertManyCalls[0]) != 500 {
			t.Errorf("first batch params length = %d, want 500", len(inserter.insertManyCalls[0]))
		}
		if len(inserter.insertManyCalls[1]) != 1 {
			t.Errorf("second batch params length = %d, want 1", len(inserter.insertManyCalls[1]))
		}
	})
}
