# Webhooks Implementation Review

## Architecture Overview

Webhooks are implemented in a clear layered way:

- **API**: `handlers/webhooks_handler.go` — CRUD over `/v1/webhooks`
- **Service**: `service/webhooks_service.go` — create/update/delete, signing key generation, cache invalidation, event publishing
- **Repository**: `repository/webhooks_repository.go` — PostgreSQL access, filters, `ListEnabledForEventType`
- **Delivery**: `service/webhook_delivery_service.go` — implements `MessagePublisher`, builds payload, signs (Standard Webhooks), sends HTTP with retries
- **Events**: `service/message_publisher.go` — `MessagePublisherManager` fans out to providers (e.g. webhooks); feedback record and webhook CRUD both call `PublishEvent`

Event flow: feedback record or webhook CRUD → `messageManager.PublishEvent` → `WebhookDeliveryService.PublishEvent` → `getWebhooksForEventType` → sign and POST to each enabled webhook URL.

---

## Strengths

1. **Standard Webhooks** — Uses `standard-webhooks` (e.g. `whsec_` keys, `webhook-id`, `webhook-signature`, `webhook-timestamp`), so subscribers can verify requests in a standard way. The `webhook-id` header is a stable identifier per event (same across all endpoints and retries), so consumers can use it as an idempotency key.

2. **Event filtering** — `event_types` (nullable array) with “empty = all events” and `ListEnabledForEventType` (including `event_types @> ARRAY[$1]`) is correct and uses a GIN index.

3. **Signing key** — Auto-generation when omitted (`generateSigningKey()` with `whsec_` + base64(32 bytes)) is correct and matches the spec.

4. **Delivery behavior** — Retries (retryablehttp, 3 retries), 10s timeout, bounded concurrency (semaphore, configurable), and non‑2xx treated as failure are all sensible.

5. **Caching** — Optional per–event-type cache with singleflight on miss avoids thundering herd and is invalidated on webhook create/update/delete via `WebhookCacheInvalidator`.

6. **API design** — List with `enabled`, `tenant_id`, `limit`, `offset`; PATCH for partial updates (including `tenant_id`, which can be set or cleared with empty string); validation and `RespondValidationError` are used consistently.

7. **Repository** — `ListEnabledForEventType` is capped (`maxWebhookListLimit` 1000), and the delivery service logs when the list hits that limit.

8. **Testing** — Integration test covers webhook CRUD and list; handlers use the same service interface, so behavior is testable.

9. **Config** — Cache and concurrency are configurable via `WEBHOOK_CACHE_*` and `WEBHOOK_DELIVERY_MAX_CONCURRENT`.

10. **Context propagation** — The request context is passed from handlers through services into `PublishEvent(ctx, event)`, so cancellation and deadlines propagate to `getWebhooksForEventType` and `sendWebhookWithJSON`. If the client disconnects or the request is cancelled, outgoing webhook HTTP calls can be cancelled accordingly.

---

## Standard Webhooks alignment

Review against the [Standard Webhooks spec](https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md):

| Spec area | Status | Notes |
|-----------|--------|--------|
| **Payload** | ✓ | `type` (full-stop delimited), `timestamp` (ISO 8601), `data`; optional `changed_fields` for updates. |
| **Headers** | ✓ | `webhook-id`, `webhook-timestamp`, `webhook-signature` set on every request; official Go library used for signing (`msg_id.timestamp.payload`, HMAC-SHA256). |
| **Signing key** | ✓ | `whsec_` + base64(32 bytes); spec allows 24–64 bytes. Keys unique per endpoint. |
| **Success / failure** | ✓ | 2xx treated as success; non-2xx (and client/network errors) trigger retries. |
| **Event types** | ✓ | Hierarchical, full-stop delimited (e.g. `feedback_record.created`); consumers can subscribe per event type. |
| **webhook-id stability** | ✓ | One stable id per event (UUID) generated in `PublishEvent` and reused for all endpoints and retries; consumers can use it as an idempotency key. |
| **Request timeout** | ⚠ | Spec recommends 15–30s; we use 10s. Consider increasing for slow consumers. |
| **Retries** | ⚠ | Spec recommends multi-day schedule with exponential backoff and jitter. We use retryablehttp with 3 retries and short backoff (immediate + a few seconds). Adequate for transient failures; no long-term retry queue. |
| **3xx redirects** | ⚠ | Spec: treat 3xx as failure and do not follow redirects (“update the webhook URL instead”). Default `http.Client` follows redirects; consider disabling for webhook delivery. |
| **410 Gone** | ⚠ | Spec: sender should disable the endpoint and stop sending. We do not special-case 410 (e.g. auto-disable webhook). |
| **429 / 502 / 504** | ○ | Spec suggests throttling. We retry like any other failure; no dedicated backoff for these codes. Optional improvement. |
| **Secret rotation** | ○ | Spec allows multiple signatures in `webhook-signature` for zero-downtime rotation. We send a single signature. Optional. |
| **Payload size** | ○ | Spec recommends keeping payloads small (e.g. under 20kb). We do not enforce or document a limit. |

---

## Potential Improvements

1. **CreateWebhookRequest `signing_key` validation**  
   `CreateWebhookRequest.SigningKey` has no `validate` tags. If the client sends a custom key, consider validating it (e.g. `omitempty,no_null_bytes,min=1,max=255`) so invalid or overshort keys are rejected; today only update validates length.

2. **Fire-and-forget and observability**  
   `PublishEvent` is fire-and-forget (goroutines, no returned errors). For production you might want at least one of:  
   - Delivery status table (e.g. per attempt: webhook_id, event, status, status_code, error, created_at) for debugging and retries, or  
   - Structured metrics (e.g. delivery attempts, success/failure, latency) so you can alert on high failure rates.

3. **Idempotency / webhook-id** — **Done.** One stable `webhook-id` per event (UUID) is generated in `PublishEvent` and passed to every `sendWebhookWithJSON` call; OpenAPI documents it as an idempotency key.

4. **Request timeout and redirects (Standard Webhooks)**  
   Spec recommends a request timeout of 15–30s; we use 10s. Consider making it configurable and defaulting to 15s. For 3xx, the spec says to treat as failure and not follow redirects; the default `http.Client` follows redirects. Use a custom client or `CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }` (or similar) so webhook delivery does not follow redirects and consumers are encouraged to update the URL.

5. **410 Gone (Standard Webhooks)**  
   The spec says that on `410 Gone` the sender should disable the webhook endpoint and stop sending. We do not special-case 410. Optional: on 410 response, set the webhook to `enabled = false` (or mark endpoint as invalid) and invalidate cache so no further deliveries are sent until the user updates the URL or re-enables.

---

## Summary

The webhooks implementation is in good shape and aligns with most of the Standard Webhooks spec: payload structure, headers, signing (HMAC-SHA256, `whsec_` keys), stable `webhook-id` per event for idempotency, 2xx semantics, event-type filtering, optional caching, bounded concurrency, retries, and clear separation of concerns. See **Standard Webhooks alignment** for a spec checklist. Remaining gaps to consider: request timeout (15–30s) and no redirect following, optional 410 handling, and create-request `signing_key` validation; plus delivery status or metrics for production operations.
