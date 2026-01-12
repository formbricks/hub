# Formbricks Hub

> Feedback record storage service for surveys, feedback, reviews, and more. Optimized for analytics and BI tools.

## Overview

Formbricks Hub is a Go-based microservice designed to collect, hub, and serve feedback records from various sources (surveys, NPS campaigns, reviews, support feedback, etc.). It provides a flexible, analytics-optimized schema that works seamlessly with BI tools like Apache Superset, Power BI, Tableau, and Looker.

### Key Features

- **Analytics-First Schema**: Each row represents a single question/response pair for easy SQL aggregations
- **Type-Safe API**: Built with Huma v2 for automatic OpenAPI 3.1 documentation
- **Flexible Data Model**: Support for text, numeric, boolean, date, and JSON responses
- **🤖 AI-Powered Enrichment (Optional)**: Automatic sentiment analysis, topic extraction, and emotion detection for text feedback using OpenAI
- **UUIDv7 Primary Keys**: Time-ordered, index-friendly identifiers
- **Webhook Events**: Real-time notifications for data changes
- **PostgreSQL 18**: Modern database with JSONB support
- **Production-Ready**: Docker support, structured logging, health checks

## Architecture

```
┌──────────────────────────────────────────────┐
│          Formbricks Hub API                │
│                                              │
│  ┌────────────────────────────────────────┐ │
│  │   Middleware Layer                     │ │
│  │   - Optional API Key Auth              │ │
│  │   - Structured Logging                 │ │
│  │   - Request/Response Tracking          │ │
│  └────────────────────────────────────────┘ │
│                                              │
│  ┌────────────────────────────────────────┐ │
│  │   Huma v2 + Chi Router                 │ │
│  │   - Auto OpenAPI docs (Scalar)         │ │
│  │   - Type-safe handlers                 │ │
│  │   - Request validation                 │ │
│  └────────────────────────────────────────┘ │
│                                              │
│  ┌────────────────────────────────────────┐ │
│  │   Domain Models (internal/models)      │ │
│  │   - Business logic layer               │ │
│  │   - API/DB conversion                  │ │
│  └────────────────────────────────────────┘ │
│                                              │
│  ┌────────────────────────────────────────┐ │
│  │   Ent ORM (internal/ent)               │ │
│  │   - Type-safe queries                  │ │
│  │   - Auto migrations                    │ │
│  │   - Code generation                    │ │
│  └────────────────────────────────────────┘ │
│                                              │
│  ┌────────────────────────────────────────┐ │
│  │   Webhook Dispatcher                   │ │
│  │   - Async events                       │ │
│  │   - Retry logic                        │ │
│  └────────────────────────────────────────┘ │
└──────────────────────────────────────────────┘
                      │
                      ▼
           ┌──────────────────┐
           │  PostgreSQL 18   │
           │  - JSONB support │
           │  - GIN indexes   │
           └──────────────────┘
```

### Layer Responsibilities

- **Middleware**: Authentication, logging, request tracking
- **API**: HTTP handling, validation, OpenAPI documentation
- **Models**: Domain logic, data transformation
- **Ent**: Database operations, migrations
- **Webhooks**: Event notifications

## Data Model

The Hub uses a **normalized schema** where each record represents a single question-answer pair. This "narrow" format is optimized for analytics, BI tools, and cross-survey analysis.

### Complete Field Reference

#### Core Identification
| Field | Type | Required | Description | Example |
|-------|------|----------|-------------|---------|
| `id` | UUID (v7) | ✅ | Time-ordered primary key | `0199f0bc-c700-7265-a8f1-3d26f2791863` |
| `collected_at` | Timestamp | ✅ | When feedback was originally collected | `2024-01-15T14:30:00Z` |
| `created_at` | Timestamp | ✅ | When record was created in database | `2024-01-15T14:35:00Z` |
| `updated_at` | Timestamp | ✅ | Last modification time | `2024-01-15T14:35:00Z` |

#### Source Tracking
| Field | Type | Required | Description | Example |
|-------|------|----------|-------------|---------|
| `source_type` | String | ✅ | Type of feedback source | `survey`, `review`, `support`, `social` |
| `source_id` | String | ❌ | External reference ID | `survey_abc123` |
| `source_name` | String | ❌ | Human-readable source name | `Q1 Product Health Survey` |

#### Question/Field Identification
| Field | Type | Required | Description | Example |
|-------|------|----------|-------------|---------|
| `field_id` | String | ✅ | Question/field identifier | `q3`, `nps_question`, `rating_ease` |
| `field_label` | String | ❌ | Human-readable question text | `How satisfied are you with Formbricks?` |
| `field_type` | String (enum) | ✅ | **Data type** (see Field Types below) | `text`, `rating`, `nps` |

#### Response Values (Type-Specific)
| Field | Type | Used For | Description |
|-------|------|----------|-------------|
| `value_text` | Text | `text`, `categorical` | Open-ended responses or selected option labels |
| `value_number` | Float | `nps`, `csat`, `rating`, `number` | Numeric scores and measurements |
| `value_boolean` | Boolean | `boolean` | True/false, yes/no responses |
| `value_date` | Timestamp | `date` | Date/datetime responses |
| `value_json` | JSONB | Complex data | Arrays, objects, nested structures |

#### AI Enrichment (Automatic for `field_type = 'text'`)
| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `sentiment` | String | Detected sentiment | `positive`, `negative`, `neutral` |
| `sentiment_score` | Float | Sentiment strength (-1 to +1) | `0.87` |
| `emotion` | String | Detected emotion | `joy`, `frustration`, `satisfaction` |
| `topics` | JSON Array | Extracted themes/keywords | `["pricing", "ui", "performance"]` |

#### Context & Metadata
| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `user_identifier` | String | Anonymous user ID or hash | `user_456`, `hash_abc123` |
| `language` | String (ISO 639-1) | Response language | `en`, `de`, `fr` |
| `metadata` | JSONB | Custom fields, device info, etc. | `{"country": "US", "device": "mobile"}` |

### Field Types Explained

The Hub uses **9 standardized field types** that map directly to analytics use cases:

#### `text` - Open-Ended Feedback
- **Use For:** Free-form comments, explanations, suggestions
- **Value Column:** `value_text`
- **AI Enrichment:** ✅ **Automatic** (sentiment, emotion, topics)
- **Analytics:** Sentiment trends, topic analysis, word clouds
- **Example:** "The UI is intuitive but the docs could be better"

#### `categorical` - Pre-Defined Options
- **Use For:** Multiple choice (single or multiple selections)
- **Value Column:** `value_text` (each selection = separate row)
- **AI Enrichment:** ❌ No (already categorized)
- **Analytics:** Frequency distribution, most popular options
- **Example:** `"Open Source"`, `"Privacy-focused"`, `"Self-hostable"`
- **Note:** For "Other" text inputs, set `metadata->>'is_other' = true` to enable enrichment

#### `nps` - Net Promoter Score
- **Use For:** "How likely are you to recommend?" (0-10 scale)
- **Value Column:** `value_number`
- **AI Enrichment:** ❌ No
- **Analytics:** NPS score calculation, promoter/passive/detractor segmentation
- **Formula:** `% Promoters (9-10) - % Detractors (0-6)`
- **Example:** `9` (Promoter)

#### `csat` - Customer Satisfaction
- **Use For:** "How satisfied are you?" (typically 1-5 or 1-7 scale)
- **Value Column:** `value_number`
- **AI Enrichment:** ❌ No
- **Analytics:** Average satisfaction, benchmarking, trends
- **Example:** `4` (on 1-5 scale)

#### `ces` - Customer Effort Score
- **Use For:** "How easy was it to...?" (typically 1-7 scale)
- **Value Column:** `value_number`
- **AI Enrichment:** ❌ No
- **Analytics:** Effort analysis, ease-of-use tracking
- **Example:** `6` (on 1-7 scale)

#### `rating` - Generic Rating Scale
- **Use For:** Star ratings, Likert scales, any ordinal scale
- **Value Column:** `value_number`
- **AI Enrichment:** ❌ No
- **Analytics:** Average ratings, distribution, correlations
- **Example:** `4.5` (stars), `7` (on 1-10 scale)

#### `number` - Numeric Measurements
- **Use For:** Counts, amounts, measurements, duration
- **Value Column:** `value_number`
- **AI Enrichment:** ❌ No
- **Analytics:** Sum, average, min/max, aggregations
- **Example:** `42` (support tickets), `12.5` (hours per week)

#### `boolean` - Yes/No
- **Use For:** Binary choices, feature flags, yes/no questions
- **Value Column:** `value_boolean`
- **AI Enrichment:** ❌ No
- **Analytics:** Percentage true/false, segmentation
- **Example:** `true` (would recommend), `false` (not a daily user)

#### `date` - Temporal Data
- **Use For:** Date/datetime responses, timestamps
- **Value Column:** `value_date`
- **AI Enrichment:** ❌ No
- **Analytics:** Time-series analysis, cohort analysis
- **Example:** `2024-01-15` (started using product)

### Schema Design: Why Narrow Format?

Each response creates **multiple rows** (one per question):

**Example: User completes 3-question survey**
```
Response ID: resp_123
User: user_456

Hubd as 3 rows:
┌─────────────┬────────────────────────────┬──────────────┬───────────────┬──────────────┐
│ field_type  │ field_label                │ value_number │ value_text    │ sentiment    │
├─────────────┼────────────────────────────┼──────────────┼───────────────┼──────────────┤
│ nps         │ Recommend?                 │ 9            │ NULL          │ NULL         │
│ rating      │ Easy to use                │ 5            │ NULL          │ NULL         │
│ text        │ What can we improve?       │ NULL         │ Better docs   │ neutral      │
└─────────────┴────────────────────────────┴──────────────┴───────────────┴──────────────┘
```

**Benefits:**
- ✅ No schema changes for new surveys
- ✅ Simple SQL aggregations (`AVG(value_number) WHERE field_type = 'nps'`)
- ✅ BI tools (Apache Superset, Power BI, Tableau, Looker) work seamlessly
- ✅ Easy cross-survey analytics

See [DATA_FORMAT_ANALYSIS.md](../../scripts/data-imports/DATA_FORMAT_ANALYSIS.md) for detailed performance analysis.

## Prerequisites

- **Go 1.22+** - [Download](https://go.dev/dl/)
- **Docker & Docker Compose** - For PostgreSQL 18
- **Make** - For convenience commands (comes with macOS/Linux)

### Verify Installation

```bash
# Check Go installation
go version  # Should show: go version go1.22 or higher

# Check Docker
docker --version && docker-compose --version

# Ensure Go bin is in PATH (add to ~/.zshrc or ~/.bashrc)
export PATH=$PATH:$(go env GOPATH)/bin
```

## Dependency Management

Go uses **`go.mod`** (like package.json in Node.js) to manage dependencies:

- **go.mod** - Lists direct dependencies and Go version
- **go.sum** - Checksums for dependency verification (like package-lock.json)

### Key Dependencies

```
entgo.io/ent v0.14.5              # ORM with code generation
github.com/danielgtaylor/huma/v2  # OpenAPI-first REST framework
github.com/go-chi/chi/v5          # HTTP router
github.com/google/uuid            # UUIDv7 support
github.com/lib/pq                 # PostgreSQL driver
```

**Note**: Dependencies are automatically downloaded when you run `go mod download` or any `go` command.

## Getting Started

### Quick Setup

```bash
# 1. Navigate to hub directory
cd apps/hub

# 2. First-time setup (installs deps, starts DB, creates .env)
make setup

# 3. Start the service
make dev
```

### Manual Setup

If you prefer step-by-step:

```bash
# 1. Navigate to hub directory
cd apps/hub

# 2. Start PostgreSQL
make docker-up

# 3. Configure environment
cp env.example .env
# Defaults work for local development

# 4. Install dependencies
go mod download
make install-deps

# 5. Start the service
make dev
```

### Verify Installation

```bash
# Check health endpoint
curl http://localhost:8080/health
# Returns: {"status":"ok"}

# Open interactive API docs (Scalar)
open http://localhost:8080/docs
```

## Quick Start

Once running, access:

- **API Docs** (Scalar): http://localhost:8080/docs - Interactive, Postman-like API testing
- **API Base**: http://localhost:8080/v1
- **OpenAPI Spec**: http://localhost:8080/openapi.json
- **Health Check**: http://localhost:8080/health

## API Endpoints

### Feedback Records

#### Create Feedback Record
```bash
POST /v1/feedback-records
Content-Type: application/json

{
  "source_type": "survey",
  "source_id": "survey-123",
  "source_name": "Q1 Customer Satisfaction",
  "field_id": "q1",
  "field_label": "How satisfied are you with our service?",
  "field_type": "rating",
  "value_number": 9,
  "metadata": {
    "country": "US",
    "device": "mobile"
  },
  "language": "en",
  "user_identifier": "user-abc-123"
}
```

#### Get Feedback Record
```bash
GET /v1/feedback-records/{id}
```

#### List Feedback Records
```bash
GET /v1/feedback-records?source_type=survey&limit=50&offset=0
```

**Query Parameters**:
- `source_type`: Filter by source type
- `source_id`: Filter by source ID
- `field_type`: Filter by field type
- `user_identifier`: Filter by user
- `since`: Filter by collected_at >= since (ISO 8601)
- `until`: Filter by collected_at <= until (ISO 8601)
- `limit`: Results per page (default: 100, max: 1000)
- `offset`: Number of results to skip

#### Update Feedback Record
```bash
PATCH /v1/feedback-records/{id}
Content-Type: application/json

{
  "value_number": 10,
  "metadata": {
    "updated": true
  }
}
```

#### Delete Feedback Record
```bash
DELETE /v1/feedback-records/{id}
```

## Environment Variables

Huma CLI automatically reads environment variables prefixed with `SERVICE_`:

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `SERVICE_DATABASE_URL` | PostgreSQL connection string | - | Yes |
| `SERVICE_PORT` | HTTP server port | `8080` | No |
| `SERVICE_HOST` | HTTP server host | `0.0.0.0` | No |
| `SERVICE_WEBHOOK_URLS` | Comma-separated webhook URLs | - | No |
| `SERVICE_ENVIRONMENT` | Environment (development/production) | `development` | No |
| `SERVICE_API_KEY` | Optional API key for authentication | - | No |
| `SERVICE_OPEN_AI_KEY` | OpenAI API key for AI features | - | No |
| `SERVICE_OPENAI_ENRICHMENT_MODEL` | AI model for enrichment | `gpt-4o-mini` | No |
| `SERVICE_OPENAI_EMBEDDING_MODEL` | AI model for embeddings | `text-embedding-3-small` | No |
| `SERVICE_ENRICHMENT_TIMEOUT` | Enrichment timeout (seconds) | `10` | No |
| `SERVICE_ENRICHMENT_WORKERS` | Number of concurrent workers | `3` | No |
| `SERVICE_ENRICHMENT_POLL_INTERVAL` | Poll interval (seconds) | `1` | No |
| `SERVICE_RATE_LIMIT_PER_IP` | Max requests/sec per IP | `100` | No |
| `SERVICE_RATE_LIMIT_BURST` | Burst allowance per IP | `200` | No |
| `SERVICE_RATE_LIMIT_GLOBAL` | Max requests/sec globally | `1000` | No |
| `SERVICE_RATE_LIMIT_GLOBAL_BURST` | Global burst allowance | `2000` | No |
| `SERVICE_LOG_LEVEL` | Log level (debug/info/warn/error) | `info` | No |

**Example `.env` file:**
```bash
SERVICE_DATABASE_URL=postgres://formbricks:formbricks_dev@localhost:5432/hub_dev?sslmode=disable
SERVICE_PORT=8080
SERVICE_WEBHOOK_URLS=http://localhost:3000/webhooks
SERVICE_API_KEY=your-secret-key-here
SERVICE_LOG_LEVEL=info
```

## Authentication

The Hub API supports optional API key authentication for securing your endpoints.

### Enabling Authentication

Set the `SERVICE_API_KEY` environment variable:

```bash
SERVICE_API_KEY=your-secret-key-here
```

When enabled, all API requests (except `/health` and `/docs`) must include the `X-API-Key` header:

```bash
curl -H "X-API-Key: your-secret-key-here" http://localhost:8080/v1/feedback-records
```

### Public Endpoints

These endpoints are always accessible without authentication:
- `GET /health` - Health check
- `GET /docs` - API documentation (Scalar)
- `GET /openapi.json` - OpenAPI specification
- `GET /openapi.yaml` - OpenAPI specification (YAML)

### Disabling Authentication

To disable authentication, simply leave `SERVICE_API_KEY` empty or unset. This is useful for:
- Local development
- Internal networks
- When using an API gateway for authentication

## Rate Limiting

The Hub API includes built-in rate limiting to protect against DoS attacks and excessive OpenAI API usage. Rate limiting is **enabled by default** with generous limits suitable for internal services.

### How It Works

The service uses a **token bucket algorithm** with two levels of protection:

1. **Per-IP Rate Limiting**: Prevents a single consumer from overwhelming the service
   - Default: 100 requests/second per IP
   - Burst: 200 requests (allows temporary spikes)

2. **Global Rate Limiting**: Protects overall service capacity
   - Default: 1000 requests/second across all IPs
   - Burst: 2000 requests

### Configuration

Configure rate limits via environment variables:

```bash
SERVICE_RATE_LIMIT_PER_IP=100        # Max requests per second per IP
SERVICE_RATE_LIMIT_BURST=200         # Burst allowance per IP
SERVICE_RATE_LIMIT_GLOBAL=1000       # Max requests per second globally
SERVICE_RATE_LIMIT_GLOBAL_BURST=2000 # Global burst allowance
```

### When Rate Limit Is Exceeded

When a rate limit is exceeded:
- Client receives `429 Too Many Requests` HTTP status
- JSON error response: `{"error":"Rate limit exceeded. Too many requests..."}`
- Detailed log entry at **warning level** with IP, path, and method

### Monitoring Rate Limits

Check logs for rate limit warnings:

```bash
# Filter for rate limit events
grep "rate limit exceeded" logs.json

# Example log entry:
{
  "level": "warn",
  "msg": "per-IP rate limit exceeded",
  "ip": "10.0.1.42",
  "path": "/v1/feedback-records",
  "method": "POST"
}
```

### Tuning Rate Limits

**Increase limits** if you have high-throughput consumers:
```bash
SERVICE_RATE_LIMIT_PER_IP=500
SERVICE_RATE_LIMIT_BURST=1000
```

**Decrease limits** to be more conservative with OpenAI costs:
```bash
SERVICE_RATE_LIMIT_PER_IP=50
SERVICE_RATE_LIMIT_BURST=100
```

**Disable rate limiting** (not recommended):
```bash
# Set very high limits effectively disables it
SERVICE_RATE_LIMIT_PER_IP=999999
SERVICE_RATE_LIMIT_GLOBAL=999999
```

## Logging

The service uses structured logging with configurable log levels via `SERVICE_LOG_LEVEL`:

- **debug**: Verbose logging including request/response details
- **info**: Standard operational logs (default)
- **warn**: Warning messages
- **error**: Error messages only

All logs are output in JSON format for easy parsing by log aggregation systems.

## Webhooks

Hub can send webhook events when data changes. Configure via `SERVICE_WEBHOOK_URLS` environment variable.

### Event Types

- `feedback_record.created`: Fired immediately when a new record is created
- `feedback_record.updated`: Fired when a record is updated
- `feedback_record.deleted`: Fired when a record is deleted
- `feedback_record.enriched`: Fired when AI enrichment completes successfully (includes full record with enrichment fields)

### Event Payload

```json
{
  "event": "feedback_record.created",
  "timestamp": "2025-10-14T12:34:56Z",
  "data": {
    "id": "01932c8a-8b9e-7000-8000-000000000001",
    "source_type": "survey",
    "field_id": "q1",
    "value_number": 9,
    ...
  }
}
```

### Retry Logic

- 3 retry attempts with exponential backoff
- 5-second timeout per request
- Failures are logged but don't block API responses

## Analytics Integration

The schema is optimized for direct SQL queries with any BI tool:

```sql
-- Average NPS score over time
SELECT 
  DATE_TRUNC('day', collected_at) as date,
  AVG(value_number) as avg_score
FROM feedback_records
WHERE field_type = 'nps'
GROUP BY date
ORDER BY date;

-- Response count by source
SELECT 
  source_name,
  COUNT(*) as response_count
FROM feedback_records
GROUP BY source_name;
```

### Connecting Your BI Tool

Connect directly to PostgreSQL using the `feedback_records` table:

- **Apache Superset**: Use the PostgreSQL connector, create datasets and dashboards
- **Power BI**: Use the PostgreSQL connector, import the table directly
- **Tableau**: Connect via PostgreSQL driver, drag-and-drop fields
- **Looker**: Define models based on the flat schema
- **Metabase/Redash**: Connect to PostgreSQL, write SQL queries

The flat schema eliminates the need for complex JSON unnesting or ETL pipelines.

## AI-Powered Enrichment (Optional)

Hub can automatically enrich open text feedback with sentiment analysis, emotion detection, and topic extraction using OpenAI's API. **Enrichment happens asynchronously in the background**, so API responses remain fast.

### Quick Setup

```bash
# Set your OpenAI API key
export SERVICE_OPEN_AI_KEY=sk-your-api-key-here

# Optional: Configure worker pool (defaults shown)
export SERVICE_ENRICHMENT_WORKERS=3
export SERVICE_ENRICHMENT_POLL_INTERVAL=1

# Start the service
make dev
```

### How It Works

1. **API receives data** → Returns immediately (fast!)
2. **Job queued** → Enrichment job added to PostgreSQL queue
3. **Background workers** → Process jobs concurrently (configurable workers)
4. **Data updated** → Enrichment fields populated when complete

When enabled, **text responses** (`field_type = 'text'`) are automatically enriched with:

- **Sentiment**: positive, negative, or neutral
- **Sentiment Score**: -1.0 to +1.0 intensity
- **Emotion**: joy, anger, frustration, sadness, neutral
- **Topics**: Array of key themes extracted from the text

**Example enriched response:**

```json
{
  "value_text": "The new dashboard is confusing and slow.",
  "sentiment": "negative",
  "sentiment_score": -0.8,
  "emotion": "frustration",
  "topics": ["dashboard", "UX", "performance"]
}
```

### Monitoring Enrichment

Check enrichment job status:

```sql
-- View pending/processing/failed jobs
SELECT status, COUNT(*) 
FROM enrichment_jobs 
GROUP BY status;

-- See recent enrichment activity
SELECT id, status, attempts, processed_at 
FROM enrichment_jobs 
ORDER BY created_at DESC 
LIMIT 10;
```

**Cost**: ~$0.12 per 1,000 feedbacks with GPT-5 mini

**Learn more**: See full documentation at `apps/docs/docs/hub/core-concepts/ai-enrichment.md`

## Development

### Make Commands

```bash
make help          # Show all available commands
make dev           # Run with hot reload (Air)
make build         # Build binary
make ent-gen       # Generate Ent code from schema
make test          # Run all tests with race detector
make test-coverage # Run tests and generate coverage report
make docker-up     # Start PostgreSQL
make docker-down   # Stop Docker services
make clean         # Clean build artifacts and test coverage files
```

### Running Tests

The service includes comprehensive tests using Go's testing framework and **testcontainers-go** for production-like testing:

```bash
# Run all tests (requires Docker running)
make test
# or
go test ./...

# Run tests with coverage
make test-coverage
# Opens coverage.html in your browser

# Run tests for specific package
go test ./internal/api/
go test ./internal/webhook/

# Run specific test
go test -v -run TestCreateFeedbackRecord ./internal/api/
```

**Test Architecture:**

✅ **Production Parity**: Tests use real **PostgreSQL 18** containers (same as production)  
✅ **Feature Coverage**: Tests JSONB, GIN indexes, and all Postgres-specific features  
✅ **Isolation**: Each test gets a fresh Postgres container  
✅ **CI/CD Ready**: Works in GitHub Actions, GitLab CI, and local development  
✅ **Industry Standard**: Uses testcontainers-go, the standard for Go integration testing  

**Test Coverage:**
- API endpoint tests using `humatest` for all CRUD operations (15 tests)
- Webhook dispatcher tests with retry logic (5 tests)
- All tests use real Postgres containers via testcontainers-go
- Single command runs everything: `go test ./...`

**Requirements:**
- Docker must be running to execute tests
- First test run downloads postgres:18-alpine image (~40MB)
- Subsequent runs reuse the cached image

**Why Real Postgres Instead of SQLite?**

We use testcontainers-go with real Postgres instead of SQLite for testing because:

- ✅ **JSONB Support**: Hub uses `JSONB` columns (`metadata`, `value_json`) with GIN indexes - Postgres-specific features
- ✅ **True Parity**: Same SQL dialect, functions, and behavior as production
- ✅ **Confidence**: If tests pass, production will work
- ✅ **No Surprises**: Catches Postgres-specific bugs during development

While tests are slightly slower (~1-2 seconds per test file for container startup), the confidence gained from testing against production-identical Postgres is invaluable.

### Managing Dependencies

#### Adding a New Dependency

```bash
# Add a new package (automatically updates go.mod)
go get github.com/some/package@latest

# Add a specific version
go get github.com/some/package@v1.2.3

# Verify and tidy dependencies
go mod tidy
```

#### Updating Dependencies

```bash
# Update all dependencies to latest minor/patch versions
go get -u ./...

# Update specific package
go get -u github.com/some/package

# Clean up unused dependencies
go mod tidy
```

#### Viewing Dependency Tree

```bash
# Show all dependencies
go list -m all

# Show why a package is needed
go mod why github.com/some/package

# Show dependency graph
go mod graph
```

#### Vendor Dependencies (Optional)

```bash
# Copy dependencies to vendor/ directory (for offline builds)
go mod vendor

# Build using vendor
go build -mod=vendor
```

### Schema Changes

Ent follows a "schema-first" approach with code generation:

1. **Edit schema**: `internal/ent/schema/feedbackrecord.go` (hand-written)
2. **Generate code**: `make ent-gen` (creates client, queries, mutations)
3. **Restart service**: `make dev` (runs migrations automatically)

**Directory Structure:**
```
internal/ent/
├── schema/                    # Hand-written schemas
│   └── feedbackrecord.go     # Your data model
├── generate.go               # Trigger for code generation
├── client.go                 # Generated - type-safe client
├── feedbackrecord.go         # Generated - entity operations
├── feedbackrecord_create.go  # Generated - create operations
├── feedbackrecord_query.go   # Generated - query operations
├── feedbackrecord_update.go  # Generated - update operations
└── migrate/                  # Generated - migrations
```

**Note**: Never edit generated files directly. Always modify schemas and regenerate.

### Adding New Endpoints

1. Add handler function in `internal/api/feedbackrecords.go`
2. Register with Huma in `RegisterFeedbackRecordRoutes()`
3. Huma automatically updates OpenAPI docs

## Docker

### Build Image

```bash
docker build -t formbricks-hub:latest .
```

### Run with Docker Compose

```bash
docker-compose up
```

This starts both PostgreSQL and the Hub service.

## Production Deployment

### Checklist

- [ ] Set `ENV=production`
- [ ] Use strong database credentials
- [ ] Enable SSL for database (`sslmode=require`)
- [ ] Configure webhook URLs
- [ ] Set up log aggregation
- [ ] Add API authentication (API gateway)
- [ ] Configure rate limiting
- [ ] Set up monitoring (Prometheus metrics)
- [ ] Database backups

### Performance Tips

- Database connection pooling is configured automatically
- Indexes are created for common query patterns
- Webhook dispatch is non-blocking
- Consider read replicas for heavy analytics workloads

## Troubleshooting

### Port Already in Use

If you get `bind: address already in use`:

```bash
# Find and kill process on port 8080
lsof -ti :8080 | xargs kill

# Or use a different port
SERVICE_PORT=8081 make dev
```

### Database Connection Refused

Ensure PostgreSQL is running:

```bash
# Check if postgres container is running
docker ps | grep postgres

# Start it if not running
make docker-up
```

### Webhooks Not Being Delivered

If you've configured `SERVICE_WEBHOOK_URLS` but webhooks aren't being sent:

```bash
# Check the startup logs for webhook initialization
# Should see: "webhook dispatcher initialized","urls":[...]

# If it says "webhooks disabled", verify the environment variable:
echo $SERVICE_WEBHOOK_URLS

# Restart the service after setting:
SERVICE_WEBHOOK_URLS=http://your-endpoint.com/webhooks make dev
```

### Tests Fail with "Cannot connect to Docker daemon"

Tests use testcontainers-go which requires Docker:

```bash
# Make sure Docker Desktop is running
# On macOS, start Docker Desktop from Applications

# Verify Docker is accessible
docker ps

# Check Docker socket permissions (Linux only)
sudo usermod -aG docker $USER
newgrp docker
```

### Tests Are Slow

Tests use real Postgres containers which take 1-2 seconds to start per test file. This is expected and provides production parity.

To speed up development, run specific tests:
```bash
go test ./internal/api/ -run TestCreateFeedbackRecord
```

### Go Command Not Found

Ensure Go is installed and in your PATH:

```bash
# Add to ~/.zshrc or ~/.bashrc
export PATH=$PATH:$(go env GOPATH)/bin

# Reload shell config
source ~/.zshrc  # or source ~/.bashrc
```

### Hot Reload Not Working

If Air isn't picking up changes:

```bash
# Restart the dev server
# Press Ctrl+C then run:
make dev
```

## Development Workflow

```bash
# Daily workflow
make dev          # Start service (Ctrl+C to stop)

# Schema changes
vim internal/ent/schema/feedbackrecord.go   # Edit hand-written schema
make ent-gen                                 # Regenerate Ent code
make dev                                     # Restart (migrations auto-run)

# Add a dependency
go get github.com/some/package@latest
go mod tidy

# Generate Ent code manually (if needed)
cd internal/ent && go generate ./generate.go
```

## Roadmap

- [ ] GraphQL API (via Ent's built-in support)
- [ ] Soft deletes with `deleted_at`
- [ ] API authentication (JWT/API keys)
- [ ] Rate limiting middleware
- [ ] Prometheus metrics
- [ ] Batch import endpoint
- [ ] Data export (CSV/JSON)
- [ ] Advanced filtering (full-text search)

## Contributing

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for contribution guidelines.

## License

See [LICENSE.md](../../LICENSE.md) for license information.
