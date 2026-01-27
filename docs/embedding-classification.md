# Embedding-Based Feedback Classification

This document explains how Formbricks Hub uses OpenAI embeddings and PostgreSQL's pgvector extension to automatically classify feedback into topics.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           FORMBRICKS HUB ARCHITECTURE                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌──────────────┐    ┌─────────────────┐    ┌────────────────────────────┐  │
│  │   Feedback   │───▶│   Hub API       │───▶│   PostgreSQL + pgvector    │  │
│  │   Ingestion  │    │   (Go)          │    │                            │  │
│  └──────────────┘    └────────┬────────┘    │  ┌──────────────────────┐  │  │
│                               │             │  │ feedback_records     │  │  │
│                               ▼             │  │ - id                 │  │  │
│                      ┌────────────────┐     │  │ - value_text         │  │  │
│                      │   OpenAI API   │     │  │ - embedding [1536]   │  │  │
│                      │ text-embedding │     │  │ - topic_id           │  │  │
│                      │   -3-small     │     │  │ - confidence         │  │  │
│                      └────────────────┘     │  └──────────────────────┘  │  │
│                                             │                            │  │
│                                             │  ┌──────────────────────┐  │  │
│                                             │  │ topics               │  │  │
│                                             │  │ - id                 │  │  │
│                                             │  │ - title              │  │  │
│                                             │  │ - level (1 or 2)     │  │  │
│                                             │  │ - parent_id          │  │  │
│                                             │  │ - embedding [1536]   │  │  │
│                                             │  └──────────────────────┘  │  │
│                                             └────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
```

## What is an Embedding?

An **embedding** is a numerical representation of text as a vector (array of numbers). OpenAI's `text-embedding-3-small` model converts any text into a **1536-dimensional vector**.

```
"The dashboard is slow"  →  [0.023, -0.041, 0.089, ..., 0.012]  (1536 numbers)
"Performance issues"     →  [0.021, -0.039, 0.091, ..., 0.010]  (1536 numbers)
```

**Key insight:** Semantically similar texts have similar vectors. We measure similarity using **cosine similarity** (0 = unrelated, 1 = identical meaning).

## Topic Hierarchy

Topics are organized in a **2-level hierarchy** using explicit `parent_id` relationships:

```
Level 1 Topics (parent_id = NULL)          Level 2 Topics (parent_id = Level 1 ID)
├── Performance                            ├── Slow Loading
│                                          ├── Dashboard Performance
│                                          └── API Response Time
├── Pricing                                ├── Feature Deprecation
│                                          ├── Plan Limitations
│                                          └── Value for Money
└── Feature Requests                       ├── AI Features
                                           ├── Custom Dashboards
                                           └── Workflows
```

**Important:** The topic hierarchy uses `parent_id` (explicit, user-defined), while feedback classification uses embedding similarity (automatic, AI-driven).

## Data Flow

### 1. Topic Creation

When a topic is created via the API:

1. The topic is stored in the database
2. An embedding is generated **asynchronously** for the topic title
3. The embedding is stored in the `topics.embedding` column

```
POST /v1/topics
{
  "title": "Dashboard Performance",
  "level": 2,
  "parent_id": "performance-topic-uuid"
}
     │
     ▼
┌─────────────────────────────────────┐
│ 1. Store topic in database          │
│ 2. Return 201 immediately           │
└─────────────────────────────────────┘
     │
     ▼ (async goroutine)
┌─────────────────────────────────────┐
│ 3. Call OpenAI API                  │
│    "Dashboard Performance"          │
│           ↓                         │
│    [0.021, -0.039, ..., 0.010]      │
│                                     │
│ 4. UPDATE topics SET embedding = ...│
└─────────────────────────────────────┘
```

### 2. Feedback Submission

When feedback is submitted:

1. The feedback record is stored immediately
2. The API returns a response (fast, doesn't wait for AI)
3. In the background, the system generates an embedding and classifies the feedback

```
POST /v1/feedback-records
{
  "value_text": "The dashboard takes forever to load",
  "field_type": "text",
  ...
}
     │
     ▼
┌─────────────────────────────────────┐
│ 1. Store feedback record            │
│ 2. Return 201 immediately           │
└─────────────────────────────────────┘
     │
     ▼ (async goroutine)
┌─────────────────────────────────────┐
│ 3. Generate embedding via OpenAI    │
│ 4. Find most similar Level 2 topic  │
│ 5. Update feedback with:            │
│    - embedding                      │
│    - topic_id                       │
│    - classification_confidence      │
└─────────────────────────────────────┘
```

### 3. Classification Process

The classification uses **pgvector** to find the most similar topic:

```sql
SELECT id, title, level, 
       1 - (embedding <=> $feedback_embedding) as similarity
FROM topics
WHERE embedding IS NOT NULL
  AND level = 2
ORDER BY embedding <=> $feedback_embedding  -- Cosine distance (ascending)
LIMIT 1
```

**Key operator:** `<=>` is pgvector's cosine distance operator

- `embedding <=> $feedback_embedding` returns the cosine distance (0 = identical, 2 = opposite)
- `1 - distance` converts to similarity score (0 = unrelated, 1 = identical)

#### Example Classification

```
Feedback: "The dashboard takes forever to load"
Embedding: [0.023, -0.041, 0.089, ..., 0.012]

┌────────────────────────┬────────────┐
│ Topic                  │ Similarity │
├────────────────────────┼────────────┤
│ Dashboard Performance  │ 0.48       │  ← Best match
│ Slow Loading           │ 0.41       │
│ API Response Time      │ 0.35       │
│ Value for Money        │ 0.12       │
└────────────────────────┴────────────┘

Result: Classified to "Dashboard Performance" with 0.48 confidence
```

### 4. Classification Threshold

A minimum similarity threshold (default: 0.30) prevents low-confidence classifications:

```go
const ClassificationThreshold = 0.30

if match.Similarity >= ClassificationThreshold {
    // Classify to this topic
} else {
    // Leave unclassified (topic_id = NULL)
}
```

## Database Schema

### topics table

```sql
CREATE TABLE topics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(255) NOT NULL,
    level INTEGER NOT NULL DEFAULT 1,      -- 1 = broad category, 2 = specific topic
    parent_id UUID REFERENCES topics(id),  -- NULL for Level 1, set for Level 2
    embedding vector(1536),                -- OpenAI embedding
    tenant_id VARCHAR(255),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Index for fast similarity search
CREATE INDEX idx_topics_embedding ON topics 
USING hnsw (embedding vector_cosine_ops);
```

### feedback_records table

```sql
CREATE TABLE feedback_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    value_text TEXT,
    embedding vector(1536),                    -- OpenAI embedding
    topic_id UUID REFERENCES topics(id),       -- Classified Level 2 topic
    classification_confidence DOUBLE PRECISION, -- Similarity score (0-1)
    -- ... other fields
);

-- Index for fast similarity search
CREATE INDEX idx_feedback_records_embedding ON feedback_records 
USING hnsw (embedding vector_cosine_ops);
```

## API Endpoints

### Topics

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/v1/topics?level=1` | List Level 1 topics |
| GET | `/v1/topics?level=2` | List Level 2 topics |
| GET | `/v1/topics/{id}/children` | Get Level 2 children of a Level 1 topic |
| POST | `/v1/topics` | Create a topic (embedding generated async) |

### Feedback Records

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/v1/feedback-records?topic_id={id}` | Get feedback for a Level 2 topic |
| POST | `/v1/feedback-records` | Create feedback (classification happens async) |

## Configuration

### Environment Variables

```bash
# Required for embedding generation
OPENAI_API_KEY=sk-...

# Optional: customize the embedding model
OPENAI_EMBEDDING_MODEL=text-embedding-3-small  # default
```

### Thresholds

In `internal/service/feedback_records_service.go`:

```go
const ClassificationThreshold = 0.30  // Minimum similarity for classification
```

## UI Flow

```
┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐
│  Level 1 Topics  │    │  Level 2 Topics  │    │  Feedback Panel  │
│                  │    │                  │    │                  │
│  ○ Pricing       │    │                  │    │                  │
│  ○ Authentication│    │                  │    │                  │
│  ● Performance   │───▶│  ● Dashboard Perf│───▶│  29 records      │
│  ○ User Exp      │    │  ○ Slow Loading  │    │                  │
│                  │    │  ○ API Response  │    │  "Dashboard is   │
│                  │    │                  │    │   slow..." (0.48)│
└──────────────────┘    └──────────────────┘    └──────────────────┘
         │                       │                      │
         │                       │                      │
    GET /topics             GET /topics/           GET /feedback-records
      ?level=1              {id}/children            ?topic_id={id}
                          (uses parent_id)
```

## Key Design Decisions

### 1. Explicit Hierarchy vs Embedding Similarity

| Relationship | Method | Reason |
|--------------|--------|--------|
| Level 1 → Level 2 | `parent_id` | User-defined, predictable, easy to manage |
| Feedback → Topic | Embedding similarity | Automatic, scales with data, semantic understanding |

We initially tried using embedding similarity for the topic hierarchy, but it was unreliable because topic titles are short and embeddings of short texts can have unexpected similarities.

### 2. Asynchronous Processing

Embedding generation and classification run in **background goroutines** to:
- Keep API responses fast (< 100ms)
- Handle OpenAI API latency (can be 500ms+)
- Allow batch processing of multiple feedback items

### 3. HNSW Index

We use HNSW (Hierarchical Navigable Small World) indexes for approximate nearest neighbor search:
- Much faster than exact search for large datasets
- Good accuracy (typically > 99% recall)
- Works on empty tables (unlike IVFFlat which needs data first)

## Troubleshooting

### Feedback Not Being Classified

1. Check if `OPENAI_API_KEY` is set
2. Check server logs for embedding errors
3. Verify topics have embeddings: `SELECT id, title, embedding IS NOT NULL FROM topics`

### Low Classification Confidence

- Topic titles may be too generic
- Consider adding more specific Level 2 topics
- Review the `ClassificationThreshold` value

### Missing Level 2 Topics in UI

- Verify `parent_id` is set correctly
- Check the `/topics/{id}/children` endpoint response
