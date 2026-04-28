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
	Delete(ctx context.Context, id uuid.UUID) error
}

// WebhooksService handles business logic for webhooks.
type WebhooksService struct {
	repo             WebhooksRepository
	publisher        MessagePublisher
	maxWebhooks      int
	urlHostBlacklist map[string]struct{}
}

// NewWebhooksService creates a new webhooks service.
// urlHostBlacklist is a set of hostnames/IPs that cannot be used as webhook URLs (SSRF mitigation); may be nil for no restriction.
func NewWebhooksService(
	repo WebhooksRepository, publisher MessagePublisher, maxWebhooks int, urlHostBlacklist map[string]struct{},
) *WebhooksService {
	return &WebhooksService{
		repo:             repo,
		publisher:        publisher,
		maxWebhooks:      maxWebhooks,
		urlHostBlacklist: urlHostBlacklist,
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

	if err := validateWebhookURLHost(ctx, req.URL, s.urlHostBlacklist); err != nil {
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

// canonicalizeHost normalizes host for blacklist lookup (trim trailing dots, lowercase).
func canonicalizeHost(host string) string {
	h := strings.TrimSpace(strings.ToLower(host))
	h = strings.TrimRight(h, ".")

	return h
}

// isPrivateOrReserved returns true if the IP is loopback, private, link-local, or unspecified.
func isPrivateOrReserved(addr netip.Addr) bool {
	addr = addr.Unmap()

	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsUnspecified()
}

// validateWebhookHost checks that the host (IP or hostname) is allowed for webhook URLs (SSRF mitigation).
// For literal IPs: rejects private/reserved ranges. For hostnames: resolves and rejects if any returned IP is disallowed.
// Always runs address checks; blacklist is applied when non-nil.
func validateWebhookHost(ctx context.Context, host string, blacklist map[string]struct{}) error {
	host = canonicalizeHost(host)
	if host == "" {
		return huberrors.NewValidationError("url", "webhook URL host is empty")
	}

	if blacklist != nil {
		if _, blocked := blacklist[host]; blocked {
			return huberrors.NewValidationError("url", "webhook URL host is not allowed (blacklisted)")
		}
	}

	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if isPrivateOrReserved(addr) {
			return huberrors.NewValidationError("url", "webhook URL host is not allowed (private/internal)")
		}

		if blacklist != nil {
			if _, blocked := blacklist[addr.String()]; blocked {
				return huberrors.NewValidationError("url", "webhook URL host is not allowed (blacklisted)")
			}
		}

		return nil
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return huberrors.NewValidationError("url", "cannot resolve webhook URL host: "+err.Error())
	}

	if len(ips) == 0 {
		return huberrors.NewValidationError("url", "webhook URL host resolves to no addresses")
	}

	for _, ipa := range ips {
		addr, ok := netip.AddrFromSlice(ipa.IP)
		if !ok {
			continue
		}

		addr = addr.Unmap()
		if isPrivateOrReserved(addr) {
			return huberrors.NewValidationError("url", "webhook URL host is not allowed (private/internal)")
		}

		if blacklist != nil {
			if _, blocked := blacklist[addr.String()]; blocked {
				return huberrors.NewValidationError("url", "webhook URL host is not allowed (blacklisted)")
			}
		}
	}

	return nil
}

// resolveWebhookHost resolves the host to allowed IPs for connection (DNS rebinding protection).
// Returns the list of IPs that pass validation, or an error if any resolved IP is disallowed.
func resolveWebhookHost(ctx context.Context, host string, blacklist map[string]struct{}) ([]netip.Addr, error) {
	host = canonicalizeHost(host)
	if host == "" {
		return nil, huberrors.NewValidationError("url", "webhook URL host is empty")
	}

	if blacklist != nil {
		if _, blocked := blacklist[host]; blocked {
			return nil, huberrors.NewValidationError("url", "webhook URL host is not allowed (blacklisted)")
		}
	}

	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if isPrivateOrReserved(addr) {
			return nil, huberrors.NewValidationError("url", "webhook URL host is not allowed (private/internal)")
		}

		if blacklist != nil {
			if _, blocked := blacklist[addr.String()]; blocked {
				return nil, huberrors.NewValidationError("url", "webhook URL host is not allowed (blacklisted)")
			}
		}

		return []netip.Addr{addr}, nil
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

		addr = addr.Unmap()
		if isPrivateOrReserved(addr) {
			return nil, huberrors.NewValidationError("url", "webhook URL host is not allowed (private/internal)")
		}

		if blacklist != nil {
			if _, blocked := blacklist[addr.String()]; blocked {
				return nil, huberrors.NewValidationError("url", "webhook URL host is not allowed (blacklisted)")
			}
		}

		allowed = append(allowed, addr)
	}

	if len(allowed) == 0 {
		return nil, huberrors.NewValidationError("url", "webhook URL host resolves to no allowed addresses")
	}

	return allowed, nil
}

// validateWebhookURLHost checks that the URL's host is allowed for webhooks (SSRF mitigation).
func validateWebhookURLHost(ctx context.Context, urlStr string, blacklist map[string]struct{}) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return huberrors.NewValidationError("url", "invalid URL: "+err.Error())
	}

	host := u.Hostname()

	return validateWebhookHost(ctx, host, blacklist)
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
		if err := validateWebhookURLHost(ctx, *req.URL, s.urlHostBlacklist); err != nil {
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

	s.publisher.PublishEventWithChangedFields(ctx, datatypes.WebhookUpdated, *webhook, req.ChangedFields())

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
	webhook, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get webhook before delete: %w", err)
	}

	if err := s.repo.Delete(ctx, id); err != nil {
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
