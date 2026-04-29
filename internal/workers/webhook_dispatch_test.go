package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

type mockDispatchRepo struct {
	webhook *models.Webhook
	err     error
	update  *models.UpdateWebhookRequest
}

func (m *mockDispatchRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
	return m.webhook, m.err
}

func (m *mockDispatchRepo) Update(_ context.Context, _ uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	m.update = req

	return nil, nil
}

type mockSender struct {
	err      error
	calls    int
	payloads []*service.WebhookPayload
}

func (m *mockSender) Send(_ context.Context, _ *models.Webhook, payload *service.WebhookPayload) error {
	m.calls++
	m.payloads = append(m.payloads, payload)

	return m.err
}

func TestWebhookDispatchWorker_Work(t *testing.T) {
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7())
	webhookID := uuid.Must(uuid.NewV7())
	tenantID := "org-123"
	args := service.WebhookDispatchArgs{
		EventID:   eventID,
		EventType: "feedback_record.created",
		Timestamp: time.Now(),
		Data:      nil,
		TenantID:  &tenantID,
		WebhookID: webhookID,
	}

	t.Run("returns nil when webhook not found", func(t *testing.T) {
		repo := &mockDispatchRepo{webhook: nil, err: errors.New("not found")}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: args}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil (no retry)", err)
		}
	})

	t.Run("returns nil when webhook disabled", func(t *testing.T) {
		repo := &mockDispatchRepo{webhook: &models.Webhook{ID: webhookID, Enabled: false}}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: args}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}
	})

	t.Run("returns nil on send success", func(t *testing.T) {
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{ID: webhookID, Enabled: true, URL: "http://x", SigningKey: "sk", TenantID: &tenantID},
		}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: args}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}

		if repo.update != nil {
			t.Error("Update should not be called on success")
		}

		if sender.calls != 1 {
			t.Errorf("Send called %d times, want 1", sender.calls)
		}
	})

	t.Run("returns nil without send when scoped webhook tenant mismatches job tenant", func(t *testing.T) {
		eventTenant := "org-other"
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{
				ID:         webhookID,
				Enabled:    true,
				URL:        "http://x",
				SigningKey: "sk",
				TenantID:   &tenantID,
			},
		}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		scopedArgs := args
		scopedArgs.TenantID = &eventTenant
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: scopedArgs}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}

		if sender.calls != 0 {
			t.Errorf("Send called %d times, want 0", sender.calls)
		}

		if repo.update != nil {
			t.Error("Update should not be called for tenant mismatch")
		}
	})

	t.Run("returns nil without send when legacy job data tenant mismatches scoped webhook", func(t *testing.T) {
		webhookTenant := "org-123"
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{
				ID:         webhookID,
				Enabled:    true,
				URL:        "http://x",
				SigningKey: "sk",
				TenantID:   &webhookTenant,
			},
		}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		scopedArgs := args
		scopedArgs.Data = map[string]any{"tenant_id": "org-other"}
		scopedArgs.TenantID = nil
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: scopedArgs}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}

		if sender.calls != 0 {
			t.Errorf("Send called %d times, want 0", sender.calls)
		}
	})

	t.Run("returns nil without send when job has no tenant boundary", func(t *testing.T) {
		webhookTenant := "org-123"
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{
				ID:         webhookID,
				Enabled:    true,
				URL:        "http://x",
				SigningKey: "sk",
				TenantID:   &webhookTenant,
			},
		}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		scopedArgs := args
		scopedArgs.Data = nil
		scopedArgs.TenantID = nil
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: scopedArgs}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}

		if sender.calls != 0 {
			t.Errorf("Send called %d times, want 0", sender.calls)
		}
	})

	t.Run("returns nil without send when job tenant conflicts with payload tenant", func(t *testing.T) {
		payloadTenant := "org-other"
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{
				ID:         webhookID,
				Enabled:    true,
				URL:        "http://x",
				SigningKey: "sk",
				TenantID:   &tenantID,
			},
		}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		scopedArgs := args
		scopedArgs.TenantID = &tenantID
		scopedArgs.Data = map[string]any{"tenant_id": payloadTenant}
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: scopedArgs}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}

		if sender.calls != 0 {
			t.Errorf("Send called %d times, want 0", sender.calls)
		}
	})

	t.Run("sends when scoped webhook tenant matches job tenant", func(t *testing.T) {
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{
				ID:         webhookID,
				Enabled:    true,
				URL:        "http://x",
				SigningKey: "sk",
				TenantID:   &tenantID,
			},
		}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		scopedArgs := args
		scopedArgs.TenantID = &tenantID
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: scopedArgs}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}

		if sender.calls != 1 {
			t.Errorf("Send called %d times, want 1", sender.calls)
		}
	})

	t.Run("sends legacy job and includes derived tenant in payload", func(t *testing.T) {
		webhookTenant := "org-123"
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{
				ID:         webhookID,
				Enabled:    true,
				URL:        "http://x",
				SigningKey: "sk",
				TenantID:   &webhookTenant,
			},
		}
		sender := &mockSender{}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
		scopedArgs := args
		scopedArgs.Data = map[string]any{"tenant_id": webhookTenant}
		scopedArgs.TenantID = nil
		job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}, Args: scopedArgs}

		err := worker.Work(ctx, job)
		if err != nil {
			t.Errorf("Work() error = %v, want nil", err)
		}

		if sender.calls != 1 {
			t.Fatalf("Send called %d times, want 1", sender.calls)
		}

		if sender.payloads[0].TenantID == nil || *sender.payloads[0].TenantID != webhookTenant {
			t.Errorf("payload tenant_id = %v, want %q", sender.payloads[0].TenantID, webhookTenant)
		}
	})

	t.Run("returns error and does not update when send fails and attempt < max", func(t *testing.T) {
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{ID: webhookID, Enabled: true, URL: "http://x", SigningKey: "sk", TenantID: &tenantID},
		}
		sender := &mockSender{err: errors.New("network error")}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
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
		repo := &mockDispatchRepo{
			webhook: &models.Webhook{ID: webhookID, Enabled: true, URL: "http://x", SigningKey: "sk", TenantID: &tenantID},
		}
		sender := &mockSender{err: errors.New("final failure")}
		worker := NewWebhookDispatchWorker(repo, sender, 15*time.Second, nil)
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
	worker := NewWebhookDispatchWorker(nil, nil, 15*time.Second, nil)
	job := &river.Job[service.WebhookDispatchArgs]{JobRow: &rivertype.JobRow{}}

	d := worker.Timeout(job)
	if d != 20*time.Second {
		t.Errorf("Timeout() = %v, want 20s (15s http + 5s buffer)", d)
	}
}
