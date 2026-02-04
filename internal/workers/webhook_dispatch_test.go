package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type mockDispatchRepo struct {
	webhook *models.Webhook
	err     error
	update  *models.UpdateWebhookRequest
}

func (m *mockDispatchRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	return m.webhook, m.err
}

func (m *mockDispatchRepo) Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	m.update = req
	return nil, nil
}

type mockSender struct {
	err error
}

func (m *mockSender) Send(ctx context.Context, webhook *models.Webhook, payload *service.WebhookPayload) error {
	return m.err
}

func TestWebhookDispatchWorker_Work(t *testing.T) {
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7())
	webhookID := uuid.Must(uuid.NewV7())
	args := service.WebhookDispatchArgs{
		EventID:   eventID,
		EventType: "feedback_record.created",
		Timestamp: time.Now().Unix(),
		Data:      nil,
		WebhookID: webhookID,
	}

	t.Run("returns nil when webhook not found", func(t *testing.T) {
		repo := &mockDispatchRepo{webhook: nil, err: errors.New("not found")}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender)
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: args}
		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil (no retry)", err)
		}
	})

	t.Run("returns nil when webhook disabled", func(t *testing.T) {
		repo := &mockDispatchRepo{webhook: &models.Webhook{ID: webhookID, Enabled: false}}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender)
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: args}
		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}
	})

	t.Run("returns nil on send success", func(t *testing.T) {
		repo := &mockDispatchRepo{webhook: &models.Webhook{ID: webhookID, Enabled: true, URL: "http://x", SigningKey: "sk"}}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender)
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: args}
		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}
		if repo.update != nil {
			t.Error("Update should not be called on success")
		}
	})

	t.Run("returns error and does not update when send fails and attempt < max", func(t *testing.T) {
		repo := &mockDispatchRepo{webhook: &models.Webhook{ID: webhookID, Enabled: true, URL: "http://x", SigningKey: "sk"}}
		sender := &mockSender{err: errors.New("network error")}
		worker := NewWebhookDispatchWorker(repo, sender)
		job := &river.Job[service.WebhookDispatchArgs]{
			JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3},
			Args:   args,
		}
		err := worker.Work(ctx, job)
		if err == nil {
			t.Error("Work() error = nil, want error")
		}
		if repo.update != nil {
			t.Error("Update should not be called when attempt < max")
		}
	})

	t.Run("updates webhook and returns error when send fails on last attempt", func(t *testing.T) {
		repo := &mockDispatchRepo{webhook: &models.Webhook{ID: webhookID, Enabled: true, URL: "http://x", SigningKey: "sk"}}
		sender := &mockSender{err: errors.New("final failure")}
		worker := NewWebhookDispatchWorker(repo, sender)
		job := &river.Job[service.WebhookDispatchArgs]{
			JobRow: &rivertype.JobRow{Attempt: 3, MaxAttempts: 3},
			Args:   args,
		}
		err := worker.Work(ctx, job)
		if err == nil {
			t.Error("Work() error = nil, want error")
		}
		if repo.update == nil {
			t.Fatal("Update should be called on last attempt failure")
		}
		if repo.update.Enabled == nil || *repo.update.Enabled {
			t.Error("Update should set Enabled = false")
		}
		if repo.update.DisabledReason == nil || *repo.update.DisabledReason != "final failure" {
			t.Errorf("DisabledReason = %v", repo.update.DisabledReason)
		}
		if repo.update.DisabledAt == nil {
			t.Error("DisabledAt should be set")
		}
	})
}

func TestWebhookDispatchWorker_Timeout(t *testing.T) {
	worker := NewWebhookDispatchWorker(nil, nil)
	job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}}
	d := worker.Timeout(job)
	if d != 25*time.Second {
		t.Errorf("Timeout() = %v, want 25s", d)
	}
}
