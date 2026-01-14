---
sidebar_position: 3
---

# Webhooks

Get notified instantly when new feedback arrives, gets updated, or is deleted. Hub sends HTTP POST requests to your endpoints, enabling real-time integrations and custom workflows.

## What You Can Build

Webhooks unlock powerful automation:
- 📢 Send Slack notifications for low NPS scores or negative sentiment
- 📊 Trigger real-time dashboard updates
- 🔄 Sync data to your data warehouse or CRM
- 🎯 Route urgent feedback to support teams automatically
- 🤖 Chain AI enrichment results into your own workflows

## Configuration

Configure webhook URLs via the `SERVICE_WEBHOOK_URLS` environment variable:

```bash
# Single webhook
SERVICE_WEBHOOK_URLS=https://api.example.com/webhooks/hub

# Multiple webhooks (comma-separated)
SERVICE_WEBHOOK_URLS=https://api.example.com/webhooks,https://analytics.example.com/events
```

Add to your `.env` file:

```bash
SERVICE_WEBHOOK_URLS=https://your-domain.com/webhooks/hub
```

Then restart the service:

```bash
make dev
```

## Event Types

Hub sends webhooks for four types of events:

### `feedback_record.created`

Triggered when new feedback is created via `POST /v1/feedback-records`.

**Common use cases:**
- 📢 Send Slack notifications for low NPS scores
- 📊 Update real-time dashboards
- 🔄 Start data processing pipelines

**Note:** For text responses, this event fires immediately when data is saved, **before** AI enrichment. Use `feedback_record.enriched` to get the AI-enriched data.

### `feedback_record.enriched`

Triggered when AI enrichment completes for text responses (sentiment, emotion, topics, embeddings).

**Common use cases:**
- 🤖 React to sentiment analysis results (e.g., alert on negative sentiment)
- 🏷️ Trigger workflows based on extracted topics
- 😊 Route feedback by emotion to appropriate teams
- 🔍 Update semantic search indexes

**Note:** This event only fires if you've configured `SERVICE_OPENAI_API_KEY` and the response has `field_type: "text"`. The payload includes the complete enriched data with `sentiment`, `sentiment_score`, `emotion`, and `topics`.

### `feedback_record.updated`

Triggered when feedback is manually updated via `PATCH /v1/feedback-records/{id}`.

**Common use cases:**
- 📝 Audit trail logging
- 🔄 Sync manual changes to external systems
- 📊 Update cached analytics

### `feedback_record.deleted`

Triggered when feedback is deleted via `DELETE /v1/feedback-records/{id}`.

**Common use cases:**
- 🧹 Maintain data consistency across systems
- 📉 Update aggregated metrics
- 📋 Compliance logging (GDPR deletion tracking)

## Event Payload

All webhooks follow a consistent format:

```json
{
  "event": "feedback_record.created",
  "timestamp": "2025-10-15T12:34:56Z",
  "data": {
    "id": "01932c8a-8b9e-7000-8000-000000000001",
    "collected_at": "2025-10-15T12:34:56Z",
    "created_at": "2025-10-15T12:34:56Z",
    "updated_at": "2025-10-15T12:34:56Z",
    "source_type": "survey",
    "source_id": "survey-123",
    "field_id": "q1",
    "field_type": "rating",
    "value_number": 9,
    "metadata": {
      "country": "US"
    },
    "language": "en",
    "user_identifier": "user-123"
  }
}
```

**Fields:**
- `event` (string): Event type - `feedback_record.created`, `feedback_record.enriched`, `feedback_record.updated`, or `feedback_record.deleted`
- `timestamp` (ISO 8601): When the event occurred
- `data` (object): Complete feedback record. For `feedback_record.enriched`, includes `sentiment`, `sentiment_score`, `emotion`, and `topics`

## Webhook Delivery

### Request Format

Hub makes an HTTP POST request to each configured webhook URL:

```http
POST /your-endpoint HTTP/1.1
Host: api.example.com
Content-Type: application/json
User-Agent: Formbricks-Hub/1.0

{
  "event": "feedback_record.created",
  "timestamp": "2025-10-15T12:34:56Z",
  "data": { ... }
}
```

### Retry Logic & Reliability

Hub uses a **worker pool architecture** to ensure reliable webhook delivery:

- **Worker pool**: 10 concurrent workers process webhooks in parallel
- **Attempts**: Up to 3 retries per webhook
- **Timeout**: 5 seconds per request
- **Backoff**: Exponential delays between retries (1s, 2s, 4s)
- **Success**: Any 2xx HTTP status code
- **Queue**: Buffered queue with backpressure handling

:::info Async Delivery
Webhook delivery is **fully asynchronous** and never blocks API responses. If your webhook is slow or fails, it won't impact the user experience. Failed webhooks are logged for debugging.
:::

:::tip Worker Pool Benefits
The worker pool ensures Hub can handle high-volume webhook traffic without memory leaks, even if your endpoints are slow or temporarily unavailable.
:::

### Expected Response

Your webhook endpoint should respond with a 2xx status code:

```http
HTTP/1.1 200 OK
Content-Type: application/json

{"received": true}
```

:::tip Respond Quickly
Your webhook must respond within **5 seconds**. For heavy processing, return 200 immediately and process the event asynchronously.
:::

## Next Steps

- [AI Enrichment →](./ai-enrichment) - Understand when AI enrichment webhooks fire
- [Data Model →](./data-model) - Complete feedback record schema
- [Authentication →](./authentication) - Secure your webhooks
- [API Reference →](../api-reference) - Explore all endpoints
