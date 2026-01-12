---
sidebar_position: 5
---

# Semantic Search

Find feedback using natural language queries. Search by meaning, not just keywords—"checkout problems" will find "payment failed", "cart issues", and "can't complete order".

## What You Can Do

With semantic search, you can:
- 🔍 **Find similar feedback** - "Show me all checkout issues" finds related problems even with different wording
- 💬 **Query in plain English** - Ask questions like a human, get relevant answers
- 🎯 **Discover patterns** - Find themes across thousands of responses instantly
- 🔄 **Support workflows** - "Find feedback like this support ticket" for faster resolution
- 📊 **Analyze themes** - Group similar feedback without manual tagging

## How It Works

Semantic search uses **vector embeddings**—numerical representations of text that capture meaning. Unlike keyword search, it understands:

- **Synonyms**: "expensive" matches "costly", "pricey", "too much money"
- **Intent**: "hard to use" matches "confusing interface", "complicated UX"
- **Context**: "checkout issues" matches "payment failed", "cart problems", "order won't submit"

### Example

**Your query:** "mobile app crashes"

**Matches found:**
- "The iOS app keeps freezing and shutting down" ✅ (88% match)
- "Android version crashes on startup" ✅ (86% match)
- "App is unstable, crashes frequently" ✅ (84% match)
- "Love the mobile feedback record!" ❌ (15% match - not relevant)

## Quick Start

### 1. Enable Embeddings

Add the embedding model to your environment:

```bash
# Required - same OpenAI key as enrichment
SERVICE_OPENAI_API_KEY=sk-your-api-key-here

# Optional - embedding model (default: text-embedding-3-small)
SERVICE_OPENAI_EMBEDDING_MODEL=text-embedding-3-small
```

That's it! Embeddings are now generated automatically for all text responses.

:::tip Default Is Great
`text-embedding-3-small` provides excellent quality for customer feedback at the lowest cost. No need to change it unless you have very technical or complex content.
:::

### 2. Search Your Feedback

Use the semantic search API:

```bash
curl "http://localhost:8080/v1/feedback-records/search?query=checkout+problems&limit=10" \
  -H "X-API-Key: your-api-key"
```

**Response:**

```json
{
  "results": [
    {
      "id": "01abc...",
      "value_text": "Payment failed during checkout. Very frustrating.",
      "field_label": "What went wrong?",
      "sentiment": "negative",
      "emotion": "frustration",
      "topics": ["checkout", "payment"],
      "similarity_score": 0.89,
      "collected_at": "2025-10-24T10:30:00Z"
    },
    {
      "id": "02def...",
      "value_text": "Can't complete my order, keeps timing out.",
      "field_label": "Issues?",
      "sentiment": "negative",
      "topics": ["order", "performance"],
      "similarity_score": 0.84,
      "collected_at": "2025-10-24T09:15:00Z"
    }
  ],
  "query": "checkout problems",
  "count": 2
}
```

## How Embeddings Are Generated

### Automatic Background Processing

When you create a text response:

1. **Feedback record saved** → API returns immediately
2. **Jobs queued** → Two background jobs created:
   - Enrichment job (sentiment, emotion, topics)
   - Embedding job (vector generation)
3. **Workers process** → Background workers pick up both jobs
4. **OpenAI called** → Text + question sent to embedding model
5. **Vector stored** → 1536-dimensional vector saved to PostgreSQL
6. **Ready to search** → Feedback record now searchable via semantic API

**Processing time:** Typically 5-15 seconds per response

### Context-Aware Embeddings

Embeddings include question context for better matching:

```
"Question: What did you like most?
Response: The fast checkout process"
```

This ensures "fast checkout" in response to "What did you like?" has different semantic meaning than "fast checkout" in response to "What needs improvement?"

### Storage with pgvector

Vectors are stored in PostgreSQL using the **pgvector** extension:
- **1536 dimensions** for `text-embedding-3-small` (default)
- **3072 dimensions** for `text-embedding-3-large`
- **HNSW index** for fast approximate nearest neighbor search
- **Cosine similarity** for matching (0.0 = no match, 1.0 = perfect match)

Vectors are **internal only**—never exposed in API responses to keep payloads lightweight.

## Search API Reference

### Endpoint

```
GET /v1/feedback-records/search
```

### Query Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | ✅ Yes | Natural language search query |
| `limit` | integer | No | Max results to return (default: 20, max: 100) |
| `source_type` | string | No | Filter by source type (e.g., "survey", "review") |
| `since` | ISO 8601 | No | Only results collected after this date |
| `until` | ISO 8601 | No | Only results collected before this date |

### Examples

**Basic search:**
```bash
GET /v1/feedback-records/search?query=login+issues
```

**Limit results:**
```bash
GET /v1/feedback-records/search?query=dashboard+feedback&limit=5
```

**Filter by source:**
```bash
GET /v1/feedback-records/search?query=bug+reports&source_type=support
```

**Date range:**
```bash
GET /v1/feedback-records/search?query=pricing+concerns&since=2025-01-01&until=2025-03-31
```

**Combine filters:**
```bash
GET /v1/feedback-records/search?query=mobile+app+crashes&source_type=review&limit=50&since=2025-01-01
```

## Use Cases

### Customer Support

**Find similar issues to current ticket:**

```bash
# Current ticket: "User can't log in with Google"
GET /v1/feedback-records/search?query=google+login+not+working&source_type=support&limit=10
```

Use results to:
- Find common solutions from past tickets
- Identify if it's a widespread issue
- Route to specialists who've handled similar cases

### Product Discovery

**Discover emerging themes:**

```bash
# What are users saying about our mobile app?
GET /v1/feedback-records/search?query=mobile+app+feedback+record&limit=100
```

Analyze results to:
- Identify pain points without manual categorization
- Track sentiment trends on specific features
- Prioritize roadmap items based on feedback volume

### Competitive Analysis

**Track competitor mentions:**

```bash
# What are users comparing us to?
GET /v1/feedback-records/search?query=competitor+comparison+features
```

Extract insights about:
- Features users wish you had
- Why users chose you over competitors
- Areas where competitors are ahead

### Trend Analysis

**Monitor specific topics over time:**

```bash
# Q1 pricing feedback
GET /v1/feedback-records/search?query=pricing+expensive+cost&since=2025-01-01&until=2025-03-31

# Q2 pricing feedback
GET /v1/feedback-records/search?query=pricing+expensive+cost&since=2025-04-01&until=2025-06-30
```

Compare `count` and `similarity_score` distributions to track changes.

## Cost Analysis

### Pricing (text-embedding-3-small)

- **Cost per 1M tokens**: $0.020
- **Average response**: ~50 words ≈ 65 tokens

**Cost per 1,000 responses**: ~$0.0013 (less than 1 cent per 1,000!)

**Cost per 100,000 responses**: ~$0.13

### At Scale

| Monthly Feedback | Embedding Cost | Per Response |
|-----------------|----------------|--------------|
| 10,000 | $0.013 | $0.0000013 |
| 100,000 | $0.13 | $0.0000013 |
| 1,000,000 | $1.30 | $0.0000013 |

**Embeddings are ~10x cheaper than enrichment** because they don't require LLM reasoning, just vector generation.

### Model Comparison

| Model | Cost (per 1M tokens) | Dimensions | Quality | Recommended For |
|-------|---------------------|------------|---------|-----------------|
| `text-embedding-3-small` | $0.020 | 1536 | Excellent | General use (default) |
| `text-embedding-3-large` | $0.130 | 3072 | Superior | Technical/complex content |

:::tip Cost Recommendation
For typical customer feedback (short-form text, simple language), `text-embedding-3-small` provides 95% of the quality at 15% of the cost. Only use `text-embedding-3-large` if you need to analyze very technical or nuanced content.
:::

## Advanced Configuration

Fine-tune embedding generation:

```bash
# Worker pool settings
SERVICE_ENRICHMENT_WORKERS=3                    # Concurrent workers (default: 3)
SERVICE_ENRICHMENT_POLL_INTERVAL=1              # Poll interval in seconds (default: 1)

# Embedding model
SERVICE_OPENAI_EMBEDDING_MODEL=text-embedding-3-small  # Default
```

**Note:** Embedding jobs share the same worker pool as enrichment jobs. If you have high volume, increase worker count.

## Monitoring

### Check Embedding Coverage

See how many responses have been embedded:

```sql
-- Embedding progress
SELECT 
  COUNT(*) FILTER (WHERE embedding IS NOT NULL) as embedded,
  COUNT(*) FILTER (WHERE embedding IS NULL) as not_embedded,
  COUNT(*) as total,
  ROUND(100.0 * COUNT(*) FILTER (WHERE embedding IS NOT NULL) / COUNT(*), 1) as percentage
FROM feedback_records
WHERE field_type = 'text' AND value_text IS NOT NULL;
```

### Monitor Embedding Jobs

Check job queue status:

```sql
-- Embedding job status
SELECT 
  status,
  COUNT(*) as count
FROM enrichment_jobs
WHERE job_type = 'embedding'
GROUP BY status;

-- Recent embedding activity
SELECT 
  id,
  status,
  created_at,
  processed_at,
  EXTRACT(EPOCH FROM (processed_at - created_at)) as processing_seconds
FROM enrichment_jobs
WHERE job_type = 'embedding'
ORDER BY created_at DESC
LIMIT 20;
```

### Verify Search Index

Check HNSW index status:

```sql
-- Index information
SELECT 
  indexname,
  indexdef
FROM pg_indexes
WHERE tablename = 'feedback_records'
  AND indexname LIKE '%embedding%';
```

## Performance

### Query Speed

- **Indexed search**: ~50-200ms for millions of records (using HNSW index)
- **Cold start**: First query may take ~500ms while warming cache
- **Concurrent**: Handles hundreds of concurrent search queries

### Accuracy vs Speed

The HNSW (Hierarchical Navigable Small Worlds) index provides **approximate nearest neighbor** search:

- **Accuracy**: ~95-99% (finds most similar results, may miss edge cases)
- **Speed**: 100-1000x faster than exact search
- **Trade-off**: Worth it for real-time search at scale

For exact results (research/analysis), query PostgreSQL directly with cosine similarity—but expect slower queries on large datasets.

## Next Steps

- [AI Enrichment →](./ai-enrichment) - Understand how sentiment/topics are extracted
- [Data Model →](./data-model) - Explore the feedback record schema
- [Webhooks →](./webhooks) - React to new feedback in real-time
- [API Reference →](../api-reference) - Explore all endpoints
