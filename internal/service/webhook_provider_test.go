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

// mockProviderRepo implements webhook listing for provider tests.
type mockProviderRepo struct {
	webhooks      []models.Webhook
	err           error
	eventType     string
	tenantID      *string
	listCallCount int
}

func (m *mockProviderRepo) ListEnabledForEventTypeAndTenant(
	_ context.Context, eventType string, tenantID *string,
) ([]models.Webhook, error) {
	m.eventType = eventType
	m.tenantID = cloneStringPointer(tenantID)
	m.listCallCount++

	if m.err != nil {
		return nil, m.err
	}

	return m.webhooks, nil
}

func TestWebhookProvider_PublishEvent(t *testing.T) {
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7())
	eventType := datatypes.FeedbackRecordCreated
	wh1 := uuid.Must(uuid.NewV7())
	wh2 := uuid.Must(uuid.NewV7())
	tenantID := "org-123"

	t.Run("inserts one job per webhook via InsertMany with correct opts", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{
			webhooks: []models.Webhook{{ID: wh1, TenantID: &tenantID}, {ID: wh2, TenantID: &tenantID}},
		}
		provider := NewWebhookProvider(inserter, repo, 3, 500, 0, 0, 0, nil)

		event := Event{
			ID:        eventID,
			Type:      eventType,
			Timestamp: time.Now(),
			Data:      map[string]string{"id": "123", "tenant_id": tenantID},
		}

		provider.PublishEvent(ctx, event)

		if repo.eventType != eventType.String() {
			t.Errorf("eventType = %q, want %q", repo.eventType, eventType.String())
		}

		if repo.tenantID == nil || *repo.tenantID != tenantID {
			t.Errorf("tenantID = %v, want %q", repo.tenantID, tenantID)
		}

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

			if args.TenantID == nil || *args.TenantID != tenantID {
				t.Errorf("param %d TenantID = %v, want %q", i, args.TenantID, tenantID)
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
		provider := NewWebhookProvider(inserter, repo, 3, 500, 0, 0, 0, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: map[string]string{"tenant_id": tenantID}}
		provider.PublishEvent(ctx, event)

		if len(inserter.insertManyCalls) != 0 {
			t.Errorf("InsertMany called %d times, want 0", len(inserter.insertManyCalls))
		}
	})

	t.Run("no InsertMany when list returns error", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{err: errors.New("db error")}
		provider := NewWebhookProvider(inserter, repo, 3, 500, 0, 0, 0, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: map[string]string{"tenant_id": tenantID}}
		provider.PublishEvent(ctx, event)

		if len(inserter.insertManyCalls) != 0 {
			t.Errorf("InsertMany called %d times, want 0", len(inserter.insertManyCalls))
		}
	})

	t.Run("when InsertMany returns error, provider logs and returns", func(t *testing.T) {
		inserter := &mockWebhookInserter{insertManyErr: errors.New("river error")}
		repo := &mockProviderRepo{
			webhooks: []models.Webhook{{ID: wh1, TenantID: &tenantID}, {ID: wh2, TenantID: &tenantID}},
		}
		provider := NewWebhookProvider(inserter, repo, 5, 500, 0, 0, 0, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: map[string]string{"tenant_id": tenantID}}
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
			webhooks[i] = models.Webhook{ID: uuid.Must(uuid.NewV7()), TenantID: &tenantID}
		}

		repo := &mockProviderRepo{webhooks: webhooks}
		provider := NewWebhookProvider(inserter, repo, 3, 500, 0, 0, 0, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: map[string]string{"tenant_id": tenantID}}
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

	t.Run("filters tenant-scoped webhooks before enqueue", func(t *testing.T) {
		tenantA := "org-123"
		tenantB := "org-other"
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{
			webhooks: []models.Webhook{
				{ID: wh1},
				{ID: wh2, TenantID: &tenantA},
				{ID: uuid.Must(uuid.NewV7()), TenantID: &tenantB},
			},
		}
		provider := NewWebhookProvider(inserter, repo, 3, 500, 0, 0, 0, nil)
		event := Event{
			ID:        eventID,
			Type:      eventType,
			Timestamp: time.Now(),
			Data:      &models.FeedbackRecord{TenantID: tenantA},
		}

		provider.PublishEvent(ctx, event)

		if repo.tenantID == nil || *repo.tenantID != tenantA {
			t.Fatalf("tenantID = %v, want %q", repo.tenantID, tenantA)
		}

		if len(inserter.insertManyCalls) != 1 {
			t.Fatalf("InsertMany called %d times, want 1", len(inserter.insertManyCalls))
		}

		params := inserter.insertManyCalls[0]
		if len(params) != 1 {
			t.Fatalf("InsertMany params length = %d, want 1", len(params))
		}

		gotWebhookIDs := map[uuid.UUID]bool{}

		for _, p := range params {
			args, ok := p.Args.(WebhookDispatchArgs)
			if !ok {
				t.Fatalf("Args type = %T, want WebhookDispatchArgs", p.Args)
			}

			if args.TenantID == nil || *args.TenantID != tenantA {
				t.Errorf("TenantID = %v, want %q", args.TenantID, tenantA)
			}

			gotWebhookIDs[args.WebhookID] = true
		}

		if !gotWebhookIDs[wh2] || gotWebhookIDs[wh1] {
			t.Errorf("enqueued webhook IDs = %v, want only matching tenant webhook", gotWebhookIDs)
		}
	})

	t.Run("tenant-less events do not query or enqueue webhooks", func(t *testing.T) {
		inserter := &mockWebhookInserter{}
		repo := &mockProviderRepo{
			webhooks: []models.Webhook{
				{ID: wh1},
				{ID: wh2, TenantID: &tenantID},
			},
		}
		provider := NewWebhookProvider(inserter, repo, 3, 500, 0, 0, 0, nil)
		event := Event{ID: eventID, Type: eventType, Timestamp: time.Now(), Data: []uuid.UUID{uuid.Must(uuid.NewV7())}}

		provider.PublishEvent(ctx, event)

		if repo.listCallCount != 0 {
			t.Fatalf("ListEnabledForEventTypeAndTenant called %d times, want 0", repo.listCallCount)
		}

		if len(inserter.insertManyCalls) != 0 {
			t.Fatalf("InsertMany called %d times, want 0", len(inserter.insertManyCalls))
		}
	})
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}

	v := *value

	return &v
}
