---
sidebar_position: 1
---

# Environment Variables

Complete reference for configuring Formbricks Hub via environment variables.

:::tip Huma Convention
Hub uses the `SERVICE_` prefix for all environment variables, following [Huma v2](https://huma.rocks/) conventions.
:::

## Required Variables

### `SERVICE_DATABASE_URL`

PostgreSQL connection string.

**Format:**
```
postgres://[user]:[password]@[host]:[port]/[database]?[params]
```

**Examples:**
```bash
# Local development
SERVICE_DATABASE_URL=postgres://formbricks:formbricks_dev@localhost:5432/hub_dev?sslmode=disable

# Production with SSL
SERVICE_DATABASE_URL=postgres://user:pass@db.example.com:5432/hub_prod?sslmode=require

# With connection pooling
SERVICE_DATABASE_URL=postgres://user:pass@pooler.example.com:5432/hub_prod?pool_max_conns=20
```

**Default:** None (required)

---

## Server Configuration

### `SERVICE_PORT`

Port for the HTTP server.

**Examples:**
```bash
SERVICE_PORT=8080  # Default
SERVICE_PORT=3000  # Custom port
```

**Default:** `8080`

---

### `SERVICE_HOST`

Host address to bind to.

**Examples:**
```bash
SERVICE_HOST=0.0.0.0  # Listen on all interfaces (default)
SERVICE_HOST=127.0.0.1  # Localhost only
SERVICE_HOST=10.0.1.5  # Specific IP
```

**Default:** `0.0.0.0`

:::warning Production Deployment
For production, use `0.0.0.0` to allow external connections. For local development, either works.
:::

---

## Security

### `SERVICE_API_KEY`

Optional API key for authentication. If set, all API requests (except `/health` and `/docs`) require the `X-API-Key` header.

**Examples:**
```bash
# No authentication (development)
# SERVICE_API_KEY=  # Commented out or empty

# With authentication (production)
SERVICE_API_KEY=your-secret-key-here
```

**Default:** Empty (authentication disabled)

**Usage:**
```bash
curl -H "X-API-Key: your-secret-key-here" \
  http://localhost:8080/v1/experiences
```

[Learn more about authentication →](../core-concepts/authentication)

---

## Webhooks

### `SERVICE_WEBHOOK_URLS`

Comma-separated list of webhook URLs to receive experience data events.

**Examples:**
```bash
# Single webhook
SERVICE_WEBHOOK_URLS=https://api.example.com/webhooks/hub

# Multiple webhooks
SERVICE_WEBHOOK_URLS=https://api.example.com/webhooks,https://analytics.example.com/events

# No webhooks
SERVICE_WEBHOOK_URLS=  # Empty
```

**Default:** Empty (no webhooks)

**Events sent:**
- `experience.created`
- `experience.updated`
- `experience.deleted`

[Learn more about webhooks →](../core-concepts/webhooks)

---

## AI Features

### `SERVICE_OPEN_AI_KEY`

OpenAI API key for AI-powered features (enrichment and embeddings). Required for both enrichment and semantic search.

**Examples:**
```bash
# With OpenAI features
SERVICE_OPEN_AI_KEY=sk-proj-xxxxxxxxxxxxxxxxxxxxx

# Without AI features
SERVICE_OPEN_AI_KEY=  # Empty
```

**Default:** Empty (AI features disabled)

:::info Getting an API Key
Get your OpenAI API key from [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
:::

---

### `SERVICE_OPENAI_ENRICHMENT_MODEL`

OpenAI chat model for sentiment, emotion, and topic analysis.

**Recommended Models:**
- `gpt-4o-mini` - Fast and cost-effective (default)
- `gpt-4o` - Higher accuracy, more expensive
- `gpt-5-mini` - Latest model (when available)

**Examples:**
```bash
SERVICE_OPENAI_ENRICHMENT_MODEL=gpt-4o-mini  # Default, recommended
SERVICE_OPENAI_ENRICHMENT_MODEL=gpt-4o       # Higher quality
```

**Default:** `gpt-4o-mini`

**Enabled when:** `SERVICE_OPEN_AI_KEY` is set and this value is non-empty.

[Learn more about AI enrichment →](../core-concepts/ai-enrichment)

---

### `SERVICE_OPENAI_EMBEDDING_MODEL`

OpenAI embeddings model for semantic search (vector generation).

**Recommended Models:**
- `text-embedding-3-small` - Cost-effective, 1536 dimensions (recommended)
- `text-embedding-3-large` - Higher accuracy, 3072 dimensions, 6.5x cost

**Cost Comparison (per 1M tokens):**
| Model | Cost | Dimensions | Use Case |
|-------|------|------------|----------|
| `text-embedding-3-small` | $0.02 | 1536 | General feedback (recommended) |
| `text-embedding-3-large` | $0.13 | 3072 | Technical/complex content |

**Examples:**
```bash
SERVICE_OPENAI_EMBEDDING_MODEL=text-embedding-3-small  # Recommended
SERVICE_OPENAI_EMBEDDING_MODEL=text-embedding-3-large  # Higher accuracy
```

**Default:** `text-embedding-3-small`

**Enabled when:** `SERVICE_OPEN_AI_KEY` is set and this value is non-empty.

:::tip Performance vs Cost
For customer feedback and survey responses, `text-embedding-3-small` provides excellent results at 1/6th the cost of the large model.
:::

[Learn more about semantic search →](../core-concepts/ai-enrichment#embeddings-and-semantic-search)

---

### `SERVICE_ENRICHMENT_TIMEOUT`

Timeout in seconds for AI API calls (both enrichment and embeddings).

**Examples:**
```bash
SERVICE_ENRICHMENT_TIMEOUT=10  # Default
SERVICE_ENRICHMENT_TIMEOUT=30  # For slower connections
```

**Default:** `10`

---

### `SERVICE_ENRICHMENT_WORKERS`

Number of concurrent background workers processing AI jobs.

**Examples:**
```bash
SERVICE_ENRICHMENT_WORKERS=3   # Default
SERVICE_ENRICHMENT_WORKERS=10  # High volume
SERVICE_ENRICHMENT_WORKERS=1   # Low volume / rate limiting
```

**Default:** `3`

:::warning Rate Limits
OpenAI has rate limits. Adjust workers based on your tier:
- Free tier: 3 requests/minute → use 1 worker
- Tier 1: 500 requests/minute → use 3-5 workers
- Tier 2+: Higher limits → use 10+ workers
:::

---

### `SERVICE_ENRICHMENT_POLL_INTERVAL`

Seconds between worker queue polls.

**Examples:**
```bash
SERVICE_ENRICHMENT_POLL_INTERVAL=1  # Default, responsive
SERVICE_ENRICHMENT_POLL_INTERVAL=5  # Lower polling frequency
```

**Default:** `1`

---

## Logging

### `SERVICE_LOG_LEVEL`

Logging verbosity level.

**Options:**
- `debug` - Detailed debugging information
- `info` - General informational messages (recommended)
- `warn` - Warning messages only
- `error` - Error messages only

**Examples:**
```bash
SERVICE_LOG_LEVEL=debug  # Development
SERVICE_LOG_LEVEL=info   # Production (recommended)
SERVICE_LOG_LEVEL=warn   # Quiet production
```

**Default:** `info`

---

### `SERVICE_ENVIRONMENT`

Environment identifier (used for logging and monitoring).

**Examples:**
```bash
SERVICE_ENVIRONMENT=development
SERVICE_ENVIRONMENT=staging
SERVICE_ENVIRONMENT=production
```

**Default:** `development`

---

## Complete Example: Development

```bash
# .env

# Database (required)
SERVICE_DATABASE_URL=postgres://formbricks:formbricks_dev@localhost:5432/hub_dev?sslmode=disable

# Server
SERVICE_PORT=8080
SERVICE_HOST=0.0.0.0

# Security (optional for dev)
SERVICE_API_KEY=

# AI Features (optional)
SERVICE_OPEN_AI_KEY=sk-proj-xxxxxxxxxxxxxxxxxxxxx
SERVICE_OPENAI_ENRICHMENT_MODEL=gpt-4o-mini
SERVICE_OPENAI_EMBEDDING_MODEL=text-embedding-3-small
SERVICE_ENRICHMENT_TIMEOUT=10
SERVICE_ENRICHMENT_WORKERS=3
SERVICE_ENRICHMENT_POLL_INTERVAL=1

# Logging
SERVICE_LOG_LEVEL=debug
SERVICE_ENVIRONMENT=development

# Webhooks (optional)
SERVICE_WEBHOOK_URLS=
```

## Complete Example: Production

```bash
# Production environment variables

# Database (required)
SERVICE_DATABASE_URL=postgres://prod_user:secure_password@db.production.com:5432/hub_prod?sslmode=require

# Server
SERVICE_PORT=8080
SERVICE_HOST=0.0.0.0

# Security (REQUIRED for production)
SERVICE_API_KEY=your-cryptographically-secure-key-here

# AI Features (recommended for production)
SERVICE_OPEN_AI_KEY=sk-proj-xxxxxxxxxxxxxxxxxxxxx
SERVICE_OPENAI_ENRICHMENT_MODEL=gpt-4o-mini
SERVICE_OPENAI_EMBEDDING_MODEL=text-embedding-3-small
SERVICE_ENRICHMENT_TIMEOUT=10
SERVICE_ENRICHMENT_WORKERS=5
SERVICE_ENRICHMENT_POLL_INTERVAL=1

# Logging
SERVICE_LOG_LEVEL=info
SERVICE_ENVIRONMENT=production

# Webhooks
SERVICE_WEBHOOK_URLS=https://api.yourdomain.com/webhooks/hub,https://analytics.yourdomain.com/events/hub
```

## Command-Line Arguments

All environment variables can also be passed as command-line arguments:

```bash
./hub \
  --database-url "postgres://..." \
  --port 8080 \
  --api-key "your-key" \
  --log-level info
```

:::tip Precedence
Command-line arguments override environment variables.
:::

## Loading Environment Variables

### Using `.env` File

```bash
# Copy example
cp env.example .env

# Edit with your values
vim .env

# Variables are automatically loaded by make dev
make dev
```

### Using Docker

```bash
docker run -d \
  -e SERVICE_DATABASE_URL="postgres://..." \
  -e SERVICE_API_KEY="your-key" \
  formbricks/hub:latest
```

### Using Docker Compose

```yaml
services:
  hub:
    image: formbricks/hub:latest
    environment:
      SERVICE_DATABASE_URL: ${SERVICE_DATABASE_URL}
      SERVICE_API_KEY: ${SERVICE_API_KEY}
      SERVICE_LOG_LEVEL: info
    env_file:
      - .env.production
```

## Troubleshooting

### "DATABASE_URL is required" Error

**Problem:** Missing `SERVICE_DATABASE_URL` environment variable.

**Solution:** Set the variable in your `.env` file:
```bash
SERVICE_DATABASE_URL=postgres://formbricks:formbricks_dev@localhost:5432/hub_dev?sslmode=disable
```

### "Cannot connect to database" Error

**Problem:** Database connection details are incorrect or database is not running.

**Solutions:**
1. Check PostgreSQL is running: `docker-compose ps`
2. Verify connection string format
3. Test connection manually:
   ```bash
   psql "postgres://formbricks:formbricks_dev@localhost:5432/hub_dev?sslmode=disable"
   ```

### API Key Not Working

**Problem:** Getting 401 errors despite setting `X-API-Key` header.

**Solutions:**
1. Verify `SERVICE_API_KEY` is set: `grep SERVICE_API_KEY .env`
2. Check header name is exact: `X-API-Key` (case-sensitive)
3. Ensure key matches exactly (no extra spaces)

## Next Steps

- [Authentication Guide](../core-concepts/authentication) - Secure your API
- [API Reference](../api-reference) - Explore endpoints
- [Architecture](./architecture) - System design and components

