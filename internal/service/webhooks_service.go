package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/cursor"
)

// WebhooksRepository defines the interface for webhooks data access.
type WebhooksRepository interface {
	Create(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error)
	GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error)
	List(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.Webhook, bool, error)
	ListAfterCursor(
		ctx context.Context, filters *models.ListWebhooksFilters,
		cursorCreatedAt time.Time, cursorID uuid.UUID,
	) ([]models.Webhook, bool, error)
	Count(ctx context.Context, filters *models.ListWebhooksFilters) (int64, error)
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error)
	Delete(ctx context.Context, id uuid.UUID) (*models.DeletedWebhook, error)
}

// WebhooksService handles business logic for webhooks.
type WebhooksService struct {
	repo        WebhooksRepository
	publisher   MessagePublisher
	maxWebhooks int
	ssrfPolicy  SSRFPolicy
}

// NewWebhooksService creates a new webhooks service.
// ssrfPolicy restricts which hosts may be used as webhook URLs (SSRF mitigation); its zero value
// still rejects private/reserved ranges.
func NewWebhooksService(
	repo WebhooksRepository, publisher MessagePublisher, maxWebhooks int, ssrfPolicy SSRFPolicy,
) *WebhooksService {
	return &WebhooksService{
		repo:        repo,
		publisher:   publisher,
		maxWebhooks: maxWebhooks,
		ssrfPolicy:  ssrfPolicy,
	}
}

// CreateWebhook creates a new webhook.
func (s *WebhooksService) CreateWebhook(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	if err := normalizeRequiredWebhookTenantID(req.TenantID); err != nil {
		return nil, err
	}

	count, err := s.repo.Count(ctx, &models.ListWebhooksFilters{})
	if err != nil {
		return nil, fmt.Errorf("count webhooks: %w", err)
	}

	if count >= int64(s.maxWebhooks) {
		return nil, huberrors.NewLimitExceededError(fmt.Sprintf("webhook limit reached (max %d)", s.maxWebhooks))
	}

	if err := validateWebhookURLHost(ctx, req.URL, s.ssrfPolicy); err != nil {
		return nil, err
	}

	if req.SigningKey == "" {
		key, err := generateSigningKey()
		if err != nil {
			return nil, fmt.Errorf("generate signing key: %w", err)
		}

		req.SigningKey = key
	} else {
		if err := validateSigningKey(req.SigningKey); err != nil {
			return nil, err
		}
	}

	webhook, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create webhook: %w", err)
	}

	s.publisher.PublishEvent(ctx, datatypes.WebhookCreated, *webhook)

	return webhook, nil
}

// validateSigningKey checks that the key is valid for Standard Webhooks (base64-decodable, correct prefix/length).
// Returns a ValidationError if the key is malformed so the client gets a 400 with a clear message.
func validateSigningKey(key string) error {
	_, err := standardwebhooks.NewWebhook(key)
	if err != nil {
		msg := "invalid for Standard Webhooks: must be base64-decodable with correct prefix and length (e.g. whsec_...): " + err.Error()

		return huberrors.NewValidationError("signing_key", msg)
	}

	return nil
}

// SigningKeySize is the number of random bytes for Standard Webhooks signing keys.
const SigningKeySize = 32

// resolveWebhookHost resolves the host to the IPs allowed for connection (SSRF mitigation).
// For a literal IP: rejects private/reserved ranges. For a hostname: resolves and rejects if ANY
// returned IP is disallowed, so a name that mixes public and internal answers cannot be used.
//
// The returned addresses are what the dialer must connect to — pinning them is what closes the
// DNS-rebinding window between validation and the request (see webhook_sender.go).
func resolveWebhookHost(ctx context.Context, host string, policy SSRFPolicy) ([]netip.Addr, error) {
	host = canonicalizeHost(host)
	if host == "" {
		return nil, huberrors.NewValidationError("url", "webhook URL host is empty")
	}

	if policy.blocked(host) {
		return nil, huberrors.NewValidationError("url", "webhook URL host is not allowed (blacklisted)")
	}

	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if err := policy.classify(addr).validationError(); err != nil {
			return nil, err
		}

		return []netip.Addr{addr.Unmap()}, nil
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, huberrors.NewValidationError("url", "cannot resolve webhook URL host: "+err.Error())
	}

	if len(ips) == 0 {
		return nil, huberrors.NewValidationError("url", "webhook URL host resolves to no addresses")
	}

	var allowed []netip.Addr

	for _, ipa := range ips {
		addr, ok := netip.AddrFromSlice(ipa.IP)
		if !ok {
			continue
		}

		if err := policy.classify(addr).validationError(); err != nil {
			return nil, err
		}

		allowed = append(allowed, addr.Unmap())
	}

	if len(allowed) == 0 {
		return nil, huberrors.NewValidationError("url", "webhook URL host resolves to no allowed addresses")
	}

	return allowed, nil
}

// validateWebhookHost checks that the host (IP or hostname) is allowed for webhook URLs.
// Thin wrapper over resolveWebhookHost that discards the addresses, so create/update-time
// validation and dial-time validation are provably the same check and cannot drift apart.
func validateWebhookHost(ctx context.Context, host string, policy SSRFPolicy) error {
	_, err := resolveWebhookHost(ctx, host, policy)

	return err
}

// validateWebhookURLHost checks that the URL's host is allowed for webhooks (SSRF mitigation).
func validateWebhookURLHost(ctx context.Context, urlStr string, policy SSRFPolicy) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return huberrors.NewValidationError("url", "invalid URL: "+err.Error())
	}

	return validateWebhookHost(ctx, u.Hostname(), policy)
}

// generateSigningKey generates a cryptographically secure signing key
// in the format expected by Standard Webhooks: "whsec_" + base64(32 random bytes).
func generateSigningKey() (string, error) {
	key := make([]byte, SigningKeySize)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("rand read: %w", err)
	}

	return "whsec_" + base64.StdEncoding.EncodeToString(key), nil
}

// GetWebhook retrieves a single webhook by ID.
func (s *WebhooksService) GetWebhook(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	webhook, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get webhook: %w", err)
	}

	return webhook, nil
}

// ListWebhooks retrieves a list of webhooks with optional filters.
// Uses cursor-based pagination: omit cursor for first page, use next_cursor for subsequent pages.
func (s *WebhooksService) ListWebhooks(ctx context.Context, filters *models.ListWebhooksFilters) (*models.ListWebhooksResponse, error) {
	if filters == nil {
		filters = &models.ListWebhooksFilters{}
	}

	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	cursorStr := strings.TrimSpace(filters.Cursor)

	var (
		webhooks []models.Webhook
		hasMore  bool
		err      error
	)

	if cursorStr != "" {
		createdAt, id, decErr := cursor.Decode(cursorStr)
		if decErr != nil {
			return nil, fmt.Errorf("decode cursor: %w", decErr)
		}

		webhooks, hasMore, err = s.repo.ListAfterCursor(ctx, filters, createdAt, id)
	} else {
		webhooks, hasMore, err = s.repo.List(ctx, filters)
	}

	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}

	// encodeLast requires non-empty webhooks when hasMore; avoid panic from invariant violation.
	if hasMore && len(webhooks) == 0 {
		return nil, fmt.Errorf("list webhooks: %w", ErrPaginationInvariantViolated)
	}

	var encodeLast func() (string, error)
	if hasMore && len(webhooks) > 0 {
		encodeLast = func() (string, error) {
			last := webhooks[len(webhooks)-1]

			return cursor.Encode(last.CreatedAt, last.ID)
		}
	}

	meta, err := BuildListPaginationMeta(filters.Limit, hasMore, encodeLast)
	if err != nil {
		return nil, fmt.Errorf("encode next cursor: %w", err)
	}

	return &models.ListWebhooksResponse{
		Data:       webhooks,
		Limit:      meta.Limit,
		NextCursor: meta.NextCursor,
	}, nil
}

// UpdateWebhook updates an existing webhook.
func (s *WebhooksService) UpdateWebhook(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	if err := normalizeOptionalWebhookTenantID(req.TenantID); err != nil {
		return nil, err
	}

	if req.URL != nil {
		if err := validateWebhookURLHost(ctx, *req.URL, s.ssrfPolicy); err != nil {
			return nil, err
		}
	}

	if req.SigningKey != nil {
		if err := validateSigningKey(*req.SigningKey); err != nil {
			return nil, err
		}
	}

	webhook, err := s.repo.Update(ctx, id, req)
	if err != nil {
		return nil, fmt.Errorf("update webhook: %w", err)
	}

	// A no-op update (no fields set) writes nothing — the repository returns the
	// current row without taking the tenant write lock — so it must not publish
	// an "updated" event either. Otherwise an empty PATCH would fire tenant-owned
	// side effects, including while the tenant is under a data purge.
	if changed := req.ChangedFields(); len(changed) > 0 {
		s.publisher.PublishEventWithChangedFields(ctx, datatypes.WebhookUpdated, *webhook, changed)
	}

	return webhook, nil
}

func normalizeRequiredWebhookTenantID(tenantID *string) error {
	normalized, err := normalizeRequiredTenantID(tenantID)
	if err != nil {
		return err
	}

	*tenantID = normalized

	return nil
}

func normalizeOptionalWebhookTenantID(tenantID *string) error {
	if tenantID == nil {
		return nil
	}

	return normalizeWebhookTenantID(tenantID)
}

func normalizeWebhookTenantID(tenantID *string) error {
	normalized, err := normalizeRequiredTenantID(tenantID)
	if err != nil {
		return err
	}

	*tenantID = normalized

	return nil
}

// DeleteWebhook deletes a webhook by ID.
// Publishes WebhookDeleted with tenant-aware deleted IDs.
func (s *WebhooksService) DeleteWebhook(ctx context.Context, id uuid.UUID) error {
	webhook, err := s.repo.Delete(ctx, id)
	if err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}

	if webhook.TenantID == nil {
		slog.Warn("webhook delete: tenant_id missing, skipping webhook event", "webhook_id", id)

		return nil
	}

	s.publisher.PublishEvent(ctx, datatypes.WebhookDeleted, models.DeletedIDsEventData{
		TenantID: *webhook.TenantID,
		IDs:      []uuid.UUID{id},
	})

	return nil
}
