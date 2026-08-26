package service

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockWebhooksRepo struct {
	count        int64
	webhook      *models.Webhook
	deleted      *models.DeletedWebhook
	deletedID    uuid.UUID
	getByIDCalls int
}

func (m *mockWebhooksRepo) Create(_ context.Context, _ *models.CreateWebhookRequest) (*models.Webhook, error) {
	// Returns the seeded webhook when set, so tests that exercise a *successful* create have
	// something to publish; a real repository never returns (nil, nil).
	return m.webhook, nil
}

func (m *mockWebhooksRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
	m.getByIDCalls++

	if m.webhook != nil {
		return m.webhook, nil
	}

	return nil, nil
}

func (m *mockWebhooksRepo) List(_ context.Context, _ *models.ListWebhooksFilters) ([]models.Webhook, bool, error) {
	return nil, false, nil
}

func (m *mockWebhooksRepo) ListAfterCursor(
	_ context.Context, _ *models.ListWebhooksFilters, _ time.Time, _ uuid.UUID,
) ([]models.Webhook, bool, error) {
	return nil, false, nil
}

func (m *mockWebhooksRepo) Count(_ context.Context, _ *models.ListWebhooksFilters) (int64, error) {
	return m.count, nil
}

func (m *mockWebhooksRepo) Update(_ context.Context, _ uuid.UUID, _ *models.UpdateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) Delete(_ context.Context, id uuid.UUID) (*models.DeletedWebhook, error) {
	m.deletedID = id

	return m.deleted, nil
}

type noopPublisher struct{}

func (noopPublisher) PublishEvent(_ context.Context, _ datatypes.EventType, _ any) {}

func (noopPublisher) PublishEventWithChangedFields(_ context.Context, _ datatypes.EventType, _ any, _ []string) {
}

type capturePublisher struct {
	eventType     datatypes.EventType
	data          any
	changedFields []string
	callCount     int
	events        []capturedEvent
}

type capturedEvent struct {
	eventType     datatypes.EventType
	data          any
	changedFields []string
}

func (p *capturePublisher) PublishEvent(_ context.Context, eventType datatypes.EventType, data any) {
	p.eventType = eventType
	p.data = data
	p.callCount++
	p.events = append(p.events, capturedEvent{eventType: eventType, data: data})
}

func (p *capturePublisher) PublishEventWithChangedFields(
	_ context.Context, eventType datatypes.EventType, data any, changedFields []string,
) {
	p.eventType = eventType
	p.data = data
	p.changedFields = changedFields
	p.callCount++
	p.events = append(p.events, capturedEvent{eventType: eventType, data: data, changedFields: changedFields})
}

func TestWebhooksService_CreateWebhook_InvalidSigningKey(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, SSRFPolicy{})
	tenantID := "org-123"

	req := &models.CreateWebhookRequest{
		URL:        "https://example.com/webhook",
		SigningKey: "not-valid",
		TenantID:   &tenantID,
		EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
	}

	_, err := svc.CreateWebhook(ctx, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestWebhooksService_UpdateWebhook_InvalidSigningKey(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, SSRFPolicy{})
	id := uuid.Must(uuid.NewV7())
	badKey := "bad_key"
	req := &models.UpdateWebhookRequest{
		SigningKey: &badKey,
	}

	_, err := svc.UpdateWebhook(ctx, id, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

// ssrfBlacklist is used by SSRF validation tests (matches default config: localhost, loopback, cloud metadata).
var ssrfBlacklist = map[string]struct{}{
	"localhost":       {},
	"127.0.0.1":       {},
	"::1":             {},
	"169.254.169.254": {},
	"blocked.local":   {},
}

func TestWebhooksService_CreateWebhook_RejectsSSRFHosts(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, SSRFPolicy{Blacklist: ssrfBlacklist})
	validKey := "whsec_" + "abcdefghijklmnopqrstuvwxyz123456"
	tenantID := "org-123"

	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"loopback IPv4 (blacklisted)", "https://127.0.0.1/webhook", "blacklisted"},
		{"loopback IPv6 (blacklisted)", "https://[::1]/webhook", "blacklisted"},
		{"private range (IP check, not in blacklist)", "https://10.0.0.1/webhook", "private/internal"},
		{"blacklisted hostname", "https://blocked.local/webhook", "blacklisted"},
		{"blacklisted IP (cloud metadata)", "https://169.254.169.254/metadata", "blacklisted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &models.CreateWebhookRequest{
				URL:        tt.url,
				SigningKey: validKey,
				TenantID:   &tenantID,
				EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
			}

			_, err := svc.CreateWebhook(ctx, req)
			if !errors.Is(err, huberrors.ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}

			var verr *huberrors.ValidationError
			if errors.As(err, &verr) && tt.wantErr != "" && !strings.Contains(verr.Message, tt.wantErr) {
				t.Errorf("error message %q does not contain %q", verr.Message, tt.wantErr)
			}
		})
	}
}

func TestWebhooksService_CreateWebhook_RequiresTenantID(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, SSRFPolicy{})

	req := &models.CreateWebhookRequest{
		URL:        "https://example.com/webhook",
		SigningKey: "whsec_" + "abcdefghijklmnopqrstuvwxyz123456",
		EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
	}

	_, err := svc.CreateWebhook(ctx, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestWebhooksService_UpdateWebhook_RejectsEmptyTenantID(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, SSRFPolicy{})
	id := uuid.Must(uuid.NewV7())
	tenantID := "   "

	req := &models.UpdateWebhookRequest{TenantID: &tenantID}

	_, err := svc.UpdateWebhook(ctx, id, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestWebhooksService_DeleteWebhook_PublishesTenantAwareDeletedEvent(t *testing.T) {
	ctx := context.Background()
	webhookID := uuid.Must(uuid.NewV7())
	tenantID := "org-123"
	repo := &mockWebhooksRepo{deleted: &models.DeletedWebhook{ID: webhookID, TenantID: &tenantID}}
	publisher := &capturePublisher{}
	svc := NewWebhooksService(repo, publisher, 10, SSRFPolicy{})

	err := svc.DeleteWebhook(ctx, webhookID)
	if err != nil {
		t.Fatalf("DeleteWebhook() error = %v", err)
	}

	if repo.deletedID != webhookID {
		t.Fatalf("deletedID = %v, want %v", repo.deletedID, webhookID)
	}

	if repo.getByIDCalls != 0 {
		t.Fatalf("GetByID called %d times, want 0; delete should return the deleted row atomically", repo.getByIDCalls)
	}

	if publisher.callCount != 1 || publisher.eventType != datatypes.WebhookDeleted {
		t.Fatalf("published event = (%d, %s), want one webhook.deleted", publisher.callCount, publisher.eventType)
	}

	data, ok := publisher.data.(models.DeletedIDsEventData)
	if !ok {
		t.Fatalf("published data type = %T, want DeletedIDsEventData", publisher.data)
	}

	if data.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", data.TenantID, tenantID)
	}

	if len(data.IDs) != 1 || data.IDs[0] != webhookID {
		t.Errorf("IDs = %v, want [%v]", data.IDs, webhookID)
	}
}

func TestWebhooksService_UpdateWebhook_RejectsSSRFHosts(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, SSRFPolicy{Blacklist: ssrfBlacklist})
	id := uuid.Must(uuid.NewV7())

	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"loopback IPv4 (blacklisted)", "https://127.0.0.1/webhook", "blacklisted"},
		{"private range", "https://10.0.0.1/webhook", "private/internal"},
		{"blacklisted hostname", "https://blocked.local/webhook", "blacklisted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.url
			req := &models.UpdateWebhookRequest{URL: &url}

			_, err := svc.UpdateWebhook(ctx, id, req)
			if !errors.Is(err, huberrors.ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

// TestWebhooksService_CreateWebhook_SSRFRangeCoverage drives the gap ranges through the real
// create path, so the classifier is proven where it is actually enforced and not just in isolation.
func TestWebhooksService_CreateWebhook_SSRFRangeCoverage(t *testing.T) {
	ctx := context.Background()
	// The repo returns a webhook so a URL that is wrongly *accepted* fails as a clean assertion
	// rather than panicking on a nil result. A panic aborts the whole test binary and hides every
	// remaining subtest — exactly when you most want to see them.
	svc := NewWebhooksService(
		&mockWebhooksRepo{count: 0, webhook: &models.Webhook{}},
		noopPublisher{}, 10, SSRFPolicy{Blacklist: ssrfBlacklist},
	)
	validKey := "whsec_" + "abcdefghijklmnopqrstuvwxyz123456"
	tenantID := "org-123"

	tests := []struct {
		name string
		url  string
	}{
		{"CGNAT / Tailscale", "https://100.64.0.1/webhook"},
		{"CGNAT MagicDNS", "https://100.100.100.100/webhook"},
		{"0.0.0.0/8", "https://0.1.2.3/webhook"},
		{"benchmarking", "https://198.18.0.1/webhook"},
		{"Azure WireServer", "https://168.63.129.16/webhook"},
		{"broadcast", "https://255.255.255.255/webhook"},
		{"multicast beyond 224/8", "https://239.255.255.250/webhook"},
		{"NAT64 to IMDS", "https://[64:ff9b::a9fe:a9fe]/webhook"},
		{"NAT64 to loopback", "https://[64:ff9b::7f00:1]/webhook"},
		{"NAT64 local-use", "https://[64:ff9b:1::1]/webhook"},
		{"6to4 to loopback", "https://[2002:7f00:1::1]/webhook"},
		{"IPv6 site-local", "https://[fec0::1]/webhook"},
		{"IPv6 multicast, site scope", "https://[ff05::1]/webhook"},
		{"IPv4-translated to loopback", "https://[::ffff:0:7f00:1]/webhook"},
		{"IPv4-translated to IMDS", "https://[::ffff:0:a9fe:a9fe]/webhook"},
		{"ORCHIDv2", "https://[2001:20::1]/webhook"},
		{"documentation (RFC 9637)", "https://[3fff::1]/webhook"},
		{"SRv6 SIDs", "https://[5f00::1]/webhook"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &models.CreateWebhookRequest{
				URL:        tt.url,
				SigningKey: validKey,
				TenantID:   &tenantID,
				EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
			}

			_, err := svc.CreateWebhook(ctx, req)
			if !errors.Is(err, huberrors.ErrValidation) {
				t.Fatalf("expected ErrValidation for %s, got %v", tt.url, err)
			}

			var verr *huberrors.ValidationError
			if errors.As(err, &verr) && !strings.Contains(verr.Message, "private/internal") {
				t.Errorf("error message %q does not contain %q", verr.Message, "private/internal")
			}
		})
	}
}

// TestWebhooksService_CreateWebhook_AllowedCIDR is the upgrade-break escape hatch: an operator
// whose receiver lives on a tailnet can re-permit that range without disabling SSRF defense.
func TestWebhooksService_CreateWebhook_AllowedCIDR(t *testing.T) {
	ctx := context.Background()
	validKey := "whsec_" + "abcdefghijklmnopqrstuvwxyz123456"
	tenantID := "org-123"

	newReq := func(url string) *models.CreateWebhookRequest {
		return &models.CreateWebhookRequest{
			URL:        url,
			SigningKey: validKey,
			TenantID:   &tenantID,
			EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
		}
	}

	// Without the allowlist the tailnet address is rejected...
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0, webhook: &models.Webhook{}}, noopPublisher{}, 10, SSRFPolicy{})
	if _, err := svc.CreateWebhook(ctx, newReq("https://100.64.0.1/webhook")); !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation without allowlist, got %v", err)
	}

	// ...and with it configured, the same URL is accepted.
	allowed := SSRFPolicy{AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10")}}
	svc = NewWebhooksService(&mockWebhooksRepo{count: 0, webhook: &models.Webhook{}}, noopPublisher{}, 10, allowed)

	if _, err := svc.CreateWebhook(ctx, newReq("https://100.64.0.1/webhook")); err != nil {
		t.Fatalf("expected the allowlisted range to be accepted, got %v", err)
	}

	// The same escape hatch has to work for the ranges added after review — an SRv6 deployment
	// whose receiver sits on a SID is blocked by default and reachable only when named.
	svc = NewWebhooksService(&mockWebhooksRepo{count: 0, webhook: &models.Webhook{}}, noopPublisher{}, 10, SSRFPolicy{})
	if _, err := svc.CreateWebhook(ctx, newReq("https://[5f00::1]/webhook")); !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation for an SRv6 SID without an allowlist, got %v", err)
	}

	srv6 := SSRFPolicy{AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("5f00::/16")}}
	svc = NewWebhooksService(&mockWebhooksRepo{count: 0, webhook: &models.Webhook{}}, noopPublisher{}, 10, srv6)

	if _, err := svc.CreateWebhook(ctx, newReq("https://[5f00::1]/webhook")); err != nil {
		t.Fatalf("expected the allowlisted SRv6 range to be accepted, got %v", err)
	}

	// The allowlist is scoped: other private ranges stay blocked.
	if _, err := svc.CreateWebhook(ctx, newReq("https://10.0.0.1/webhook")); !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected RFC1918 to stay blocked with a CGNAT allowlist, got %v", err)
	}
}
