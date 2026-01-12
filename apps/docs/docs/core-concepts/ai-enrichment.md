# AI Enrichment

Turn raw text feedback into actionable insights automatically. Hub uses OpenAI to extract sentiment, emotion, and topics from every text response—no manual tagging required.

## What You Can Do

With AI enrichment, you can:
- 🚨 **Alert on negative sentiment** - Get notified when customers are frustrated or angry
- 🎯 **Route by emotion** - Send feedback to the right team based on emotional tone
- 📊 **Analyze trends** - Track sentiment over time without manual categorization
- 🏷️ **Discover themes** - Automatically identify recurring topics (pricing, UI, bugs)
- 🔍 **Query by feeling** - Find all "frustrated" customers or "joyful" feedback

## What Gets Enriched

Hub enriches **only text responses** (`field_type = 'text'`). Ratings, NPS scores, and other field types pass through unchanged.

For each text response, Hub automatically extracts:

| Field | Type | Description | Example Values |
|-------|------|-------------|----------------|
| `sentiment` | string | Overall polarity | `"positive"`, `"negative"`, `"neutral"`, `"mixed"` |
| `sentiment_score` | float | Confidence (-1.0 to +1.0) | `-0.8` (very negative), `0.6` (positive) |
| `emotion` | string | Primary emotional tone | `"joy"`, `"frustration"`, `"anger"`, `"sadness"`, `"confusion"` |
| `topics` | array | Key themes/subjects | `["pricing", "dashboard", "performance", "support"]` |

## Quick Start

### 1. Get an OpenAI API Key

Sign up at [platform.openai.com](https://platform.openai.com) and generate an API key.

**Cost:** ~$0.015 per 1,000 text responses (very affordable at scale)

### 2. Configure Hub

Add your OpenAI API key to your environment:

```bash
# Required - enables AI enrichment
SERVICE_OPENAI_API_KEY=sk-your-api-key-here

# Optional - customize the enrichment model (default: gpt-4o-mini)
SERVICE_OPENAI_ENRICHMENT_MODEL=gpt-4o-mini
```

That's it! Enrichment is now enabled automatically for all text responses.

:::tip Start Simple
The default configuration works great for most use cases. Advanced tuning options are available below if needed.
:::

### 3. Test It Out

Create a text response:

```bash
curl -X POST http://localhost:8080/v1/feedback-records \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-api-key" \
  -d '{
    "source_type": "survey",
    "field_id": "feedback",
    "field_label": "What can we improve?",
    "field_type": "text",
    "value_text": "The new dashboard is confusing and slow. Really frustrating!"
  }'
```

Within ~5-15 seconds, the enrichment completes. Query the feedback record to see the results:

```bash
curl http://localhost:8080/v1/feedback-records/{id} \
  -H "X-API-Key: your-api-key"
```

Response with AI enrichment:
```json
{
  "id": "01abc...",
  "value_text": "The new dashboard is confusing and slow. Really frustrating!",
  "sentiment": "negative",
  "sentiment_score": -0.8,
  "emotion": "frustration",
  "topics": ["dashboard", "ui_design", "performance"],
  "collected_at": "2025-10-24T10:30:00Z",
  "created_at": "2025-10-24T10:30:00Z",
  "updated_at": "2025-10-24T10:30:12Z"
}
```

## How It Works

### Asynchronous Processing

Enrichment happens **asynchronously in the background**, so your API stays fast:

1. **POST request arrives** → Feedback record saved immediately (~20-50ms)
2. **Return 201 Created** → API responds instantly
3. **Job queued** → Enrichment job added to PostgreSQL queue
4. **Workers process** → Background workers pick up pending jobs
5. **Call OpenAI** → Text sent to `gpt-4o-mini` for analysis
6. **Extract insights** → Parse sentiment, emotion, and topics
7. **Update feedback record** → Enrichment fields populated
8. **Fire webhook** → `feedback_record.enriched` event dispatched

**Result:** Your users never wait for AI processing. Enrichment typically completes within 5-15 seconds.

### Worker Pool Architecture

Hub uses a **pool of concurrent workers** to process enrichment jobs efficiently:

- **3 workers by default** - Process multiple jobs in parallel
- **PostgreSQL-backed queue** - Reliable job storage with retries
- **Graceful error handling** - Failed enrichments never block your API
- **Automatic retries** - Transient failures (network issues, rate limits) are retried

### What Gets Sent to OpenAI

Hub sends the text response with question context (if available) for better topic extraction:

**With question context:**
```
Question: What can we improve?
Response: The checkout process is confusing and takes too long
```

**Without question context:**
```
The checkout process is confusing and takes too long
```

Including the question helps the AI extract more accurate topics. For example, "It's great!" will generate different topics depending on whether it's answering "What did you like?" vs "How was checkout?"

### Reliability & Error Handling

Hub is designed to **never fail** because of AI enrichment:

- ❌ **OpenAI timeout?** → Feedback record saved, enrichment skipped
- ❌ **API rate limit?** → Job retried later  
- ❌ **Network error?** → Job retried with backoff
- ❌ **Invalid response?** → Enrichment skipped, logged for debugging
- ❌ **No API key set?** → Enrichment silently disabled

Your data is **always saved**, regardless of enrichment status.

## Webhooks

When enrichment completes, Hub automatically sends an `feedback_record.enriched` webhook to all configured endpoints.

### Event Flow

```
1. POST /v1/feedback-records
   ↓
2. feedback_record.created webhook fires immediately (no enrichment yet)
   ↓
3. Background enrichment processes (5-15 seconds)
   ↓
4. feedback_record.enriched webhook fires (with sentiment, emotion, topics)
```

### Use Cases

- **Alert on negative sentiment** - Slack notification for `sentiment_score < -0.5`
- **Route by emotion** - Send "anger" feedback to escalation team
- **Update dashboards** - Refresh real-time sentiment charts
- **Trigger workflows** - Start automated follow-up for specific topics

### Example Payload

```json
{
  "event": "feedback_record.enriched",
  "timestamp": "2025-10-24T10:30:12Z",
  "data": {
    "id": "01abc...",
    "value_text": "The new dashboard is amazing!",
    "sentiment": "positive",
    "sentiment_score": 0.95,
    "emotion": "joy",
    "topics": ["dashboard", "ui_design"],
    "field_label": "What do you think?",
    "collected_at": "2025-10-24T10:30:00Z"
  }
}
```

[Learn more about webhooks →](./webhooks)

## Monitoring Enrichment

### Check Enrichment Status

Query the `enrichment_jobs` table to monitor background processing:

```sql
-- View job status counts
SELECT status, COUNT(*) as count
FROM enrichment_jobs
GROUP BY status;

-- Check recent enrichment activity
SELECT 
  id,
  status,
  attempts,
  created_at,
  processed_at,
  EXTRACT(EPOCH FROM (processed_at - created_at)) as processing_seconds
FROM enrichment_jobs
ORDER BY created_at DESC
LIMIT 10;

-- Find failed jobs
SELECT 
  id,
  feedback_record_id,
  error,
  attempts,
  created_at
FROM enrichment_jobs
WHERE status = 'failed'
ORDER BY created_at DESC;
```

### Enrichment Progress

Check how many feedback records have been enriched:

```sql
-- Overall enrichment progress
SELECT 
  COUNT(*) FILTER (WHERE sentiment IS NOT NULL) as enriched,
  COUNT(*) FILTER (WHERE sentiment IS NULL) as not_enriched,
  COUNT(*) as total,
  ROUND(100.0 * COUNT(*) FILTER (WHERE sentiment IS NOT NULL) / COUNT(*), 1) as enrichment_percentage
FROM feedback_records
WHERE field_type = 'text' AND value_text IS NOT NULL;
```

### Worker Performance

Monitor worker throughput:

```sql
-- Jobs processed per hour
SELECT 
  DATE_TRUNC('hour', processed_at) as hour,
  COUNT(*) as jobs_completed,
  AVG(EXTRACT(EPOCH FROM (processed_at - created_at))) as avg_processing_seconds
FROM enrichment_jobs
WHERE status = 'completed'
  AND processed_at >= NOW() - INTERVAL '24 hours'
GROUP BY hour
ORDER BY hour DESC;
```

### Logs

Workers log enrichment activity:

```
INFO processing enrichment job worker_id=1 job_id=... feedback_record_id=...
INFO enrichment completed successfully worker_id=1 sentiment=negative
WARN enrichment failed worker_id=2 error="context deadline exceeded"
```

## Analyzing Enriched Data

With sentiment, emotion, and topics automatically extracted, you can build powerful analytics queries.

### Find Urgent Issues

```sql
-- Very negative feedback with anger/frustration
SELECT 
  value_text,
  sentiment_score,
  emotion,
  topics,
  collected_at
FROM feedback_records
WHERE sentiment_score < -0.7  -- Very negative
  AND emotion IN ('anger', 'frustration')
ORDER BY collected_at DESC
LIMIT 20;
```

### Track Sentiment Trends

```sql
-- Weekly sentiment over time
SELECT 
  DATE_TRUNC('week', collected_at) as week,
  AVG(sentiment_score) as avg_sentiment,
  COUNT(*) as feedback_count
FROM feedback_records
WHERE sentiment_score IS NOT NULL
GROUP BY week
ORDER BY week DESC;
```

### Discover Popular Topics

```sql
-- Most mentioned topics with average sentiment
SELECT 
  unnest(topics) as topic,
  COUNT(*) as mentions,
  ROUND(AVG(sentiment_score)::numeric, 2) as avg_sentiment
FROM feedback_records
WHERE topics IS NOT NULL
GROUP BY topic
ORDER BY mentions DESC
LIMIT 20;
```

### Emotion Breakdown

```sql
-- Distribution of emotions
SELECT 
  emotion,
  COUNT(*) as count,
  ROUND(100.0 * COUNT(*) / SUM(COUNT(*)) OVER (), 1) as percentage
FROM feedback_records
WHERE emotion IS NOT NULL
GROUP BY emotion
ORDER BY count DESC;
```

### Topic + Sentiment Correlation

```sql
-- Which topics correlate with negative sentiment?
SELECT 
  unnest(topics) as topic,
  COUNT(*) FILTER (WHERE sentiment = 'negative') as negative_count,
  COUNT(*) as total_count,
  ROUND(100.0 * COUNT(*) FILTER (WHERE sentiment = 'negative') / COUNT(*), 1) as negative_percentage
FROM feedback_records
WHERE topics IS NOT NULL
GROUP BY topic
ORDER BY negative_percentage DESC
LIMIT 20;
```

## Cost Analysis

### Pricing (gpt-4o-mini)

- **Input tokens**: $0.150 per 1M tokens
- **Output tokens**: $0.600 per 1M tokens

### Typical Usage

Average text response: ~50 words ≈ 65 input tokens + 50 output tokens

**Cost per 1,000 responses**: ~$0.015 ($0.01 USD + $0.03 USD output)

**Cost per 100,000 responses**: ~$1.50

### At Scale

| Monthly Feedback | Enrichment Cost | Per Response |
|-----------------|-----------------|--------------|
| 10,000 | $0.15 | $0.000015 |
| 100,000 | $1.50 | $0.000015 |
| 1,000,000 | $15.00 | $0.000015 |

:::tip Cost Optimization
For high volumes (1M+ responses/month), consider:
- Using prompt caching (10x cheaper on repeated context)
- Filtering only critical feedback for enrichment
- Batching requests where possible
:::


## Advanced Configuration

For fine-tuning enrichment behavior:

```bash
# Worker pool settings
SERVICE_ENRICHMENT_WORKERS=3                    # Concurrent workers (default: 3)
SERVICE_ENRICHMENT_POLL_INTERVAL=1              # Poll interval in seconds (default: 1)

# OpenAI settings
SERVICE_ENRICHMENT_TIMEOUT=10                   # API timeout in seconds (default: 10)
SERVICE_OPENAI_ENRICHMENT_MODEL=gpt-4o-mini     # Model choice (default)
```

### Worker Pool Sizing

- **Low volume** (< 1,000/hour): 1-2 workers sufficient
- **Medium volume** (1,000-10,000/hour): 3-5 workers recommended
- **High volume** (10,000+/hour): 5-10 workers + increase poll interval

### Model Selection

| Model | Cost | Speed | Quality | Recommended For |
|-------|------|-------|---------|-----------------|
| `gpt-4o-mini` | $ | Fast | Excellent | General use (default) |
| `gpt-4o` | $$$ | Slower | Superior | Complex/nuanced feedback |

## Privacy & Compliance

**Important considerations when using OpenAI:**

- ✅ **Anonymize PII** - Remove names, emails, phone numbers before storing
- ✅ **Review OpenAI's policies** - [Data usage policy](https://openai.com/policies/api-data-usage-policies)
- ✅ **Check regional regulations** - GDPR, CCPA, etc.
- ✅ **API data not used for training** - OpenAI doesn't train on API data (per their policy)

**For highly sensitive feedback**: Consider disabling enrichment or self-hosting open-source models.

## Troubleshooting

### Enrichment Not Running

**Check API key configuration:**

```bash
echo $SERVICE_OPENAI_API_KEY
```

**Look for startup logs:**

```
INFO text enrichment enabled model=gpt-4o-mini
```

If you see `text enrichment disabled`, the API key isn't set.

**Test manually:**

```bash
curl -X POST http://localhost:8080/v1/feedback-records \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-key" \
  -d '{
    "source_type": "survey",
    "field_id": "test",
    "field_type": "text",
    "value_text": "This is amazing!"
  }'
```

Wait 10-15 seconds, then query the feedback record. Check for `sentiment`, `emotion`, and `topics`.

### Slow Enrichment

**Symptom:** Jobs taking 30+ seconds

**Common causes:**
- OpenAI API slowness ([check status](https://status.openai.com))
- Network latency
- Rate limiting

**Solutions:**
- Increase workers: `SERVICE_ENRICHMENT_WORKERS=5`
- Reduce timeout: `SERVICE_ENRICHMENT_TIMEOUT=5`

### Rate Limit Errors

**Symptom:** Logs show "429 Too Many Requests"

**Solutions:**
1. Upgrade your OpenAI tier: [platform.openai.com/account/limits](https://platform.openai.com/account/limits)
2. Reduce concurrent workers: `SERVICE_ENRICHMENT_WORKERS=1`
3. Add delays between requests (contact us for custom configuration)

## Next Steps

- [Webhooks →](./webhooks) - React to enriched data in real-time
- [Data Model →](./data-model) - Understanding the enriched fields
- [Semantic Search →](./semantic-search) - Query feedback with natural language
- [API Reference →](../api-reference) - Explore all endpoints
