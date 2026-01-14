---
sidebar_position: 2
---

# Quick Start Guide

Get the Formbricks Hub running with Docker in under 5 minutes.

## Prerequisites

- **Docker** and **Docker Compose**: [Install Docker Desktop](https://www.docker.com/products/docker-desktop/)
- **Text Editor**: For editing the `.env` file

## Installation

### Option 1: Docker Compose (Recommended)

The easiest way to run Formbricks Hub with all dependencies.

#### 1. Download the Configuration

Create a new directory and download the production Docker Compose file:

```bash
mkdir formbricks-hub && cd formbricks-hub
curl -o docker-compose.yml https://raw.githubusercontent.com/formbricks/hub/main/docker-compose.prod.yml
```

Or create it manually:

```yaml title="docker-compose.yml"
services:
  postgres:
    image: postgres:18-alpine
    container_name: formbricks_hub_postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: formbricks
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-formbricks_secure_password}
      POSTGRES_DB: hub
    ports:
      - '5432:5432'
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ['CMD-SHELL', 'pg_isready -U formbricks -d hub']
      interval: 10s
      timeout: 5s
      retries: 5
    networks:
      - formbricks_hub

  hub:
    image: ghcr.io/formbricks/hub:latest
    container_name: formbricks_hub_api
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      SERVICE_DATABASE_URL: postgresql://formbricks:${POSTGRES_PASSWORD:-formbricks_secure_password}@postgres:5432/hub?sslmode=disable
      SERVICE_API_KEY: ${SERVICE_API_KEY}
      SERVICE_PORT: ${SERVICE_PORT:-8080}
      SERVICE_HOST: 0.0.0.0
      SERVICE_ENVIRONMENT: ${SERVICE_ENVIRONMENT:-production}
      SERVICE_LOG_LEVEL: ${SERVICE_LOG_LEVEL:-info}
      SERVICE_OPENAI_API_KEY: ${SERVICE_OPENAI_API_KEY:-}
      SERVICE_OPENAI_ENRICHMENT_MODEL: ${SERVICE_OPENAI_ENRICHMENT_MODEL:-gpt-4o-mini}
      SERVICE_OPENAI_EMBEDDING_MODEL: ${SERVICE_OPENAI_EMBEDDING_MODEL:-text-embedding-3-small}
      SERVICE_WEBHOOK_URLS: ${SERVICE_WEBHOOK_URLS:-}
    ports:
      - '8080:8080'
    healthcheck:
      test: ['CMD', 'wget', '--no-verbose', '--tries=1', '--spider', 'http://localhost:8080/health']
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
    networks:
      - formbricks_hub

volumes:
  postgres_data:
    driver: local

networks:
  formbricks_hub:
    driver: bridge
```

#### 2. Configure Environment Variables

Create a `.env` file in the same directory:

```bash title=".env"
# Required: Secure your PostgreSQL database
POSTGRES_PASSWORD=your_secure_postgres_password_here

# Required: API authentication key
SERVICE_API_KEY=your_secure_api_key_here

# Optional: OpenAI API for AI enrichment and semantic search
# If not provided, text responses will be stored without AI analysis
SERVICE_OPENAI_API_KEY=sk-your-openai-api-key-here
SERVICE_OPENAI_ENRICHMENT_MODEL=gpt-4o-mini
SERVICE_OPENAI_EMBEDDING_MODEL=text-embedding-3-small

# Optional: Server configuration
SERVICE_PORT=8080
SERVICE_ENVIRONMENT=production
SERVICE_LOG_LEVEL=info
```

:::tip Generate Secure Keys
Generate secure passwords and API keys:
```bash
# For POSTGRES_PASSWORD
openssl rand -base64 32

# For SERVICE_API_KEY
openssl rand -base64 32
```
:::

#### 3. Start the Services

```bash
docker-compose up -d
```

This will:
- Pull the latest Formbricks Hub image from GitHub Container Registry
- Start PostgreSQL with persistent storage
- Run database migrations automatically
- Start the Hub API on port 8080

#### 4. Verify Installation

Check that both services are healthy:

```bash
docker-compose ps
```

Expected output:
```
NAME                        STATUS              PORTS
formbricks_hub_api        Up (healthy)        0.0.0.0:8080->8080/tcp
formbricks_hub_postgres   Up (healthy)        0.0.0.0:5432->5432/tcp
```

Test the health endpoint:

```bash
curl http://localhost:8080/health
```

Expected response:
```json
{"status":"ok"}
```

### Option 2: Docker Run (Minimal)

If you have an existing PostgreSQL instance, run just the Hub API:

```bash
docker run -d \
  --name formbricks-hub \
  -p 8080:8080 \
  -e SERVICE_DATABASE_URL="postgresql://user:password@host:5432/hub?sslmode=disable" \
  -e SERVICE_API_KEY="your-secret-key" \
  -e SERVICE_OPENAI_API_KEY="sk-your-openai-key" \
  -e SERVICE_LOG_LEVEL="info" \
  ghcr.io/formbricks/hub:latest
```

:::tip
Omit `SERVICE_OPENAI_API_KEY` if you don't need AI enrichment features.
:::

## Making Your First API Call

### 1. Create a Feedback Record

```bash
curl -X POST http://localhost:8080/v1/feedback-records \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your_secure_api_key_here" \
  -d '{
    "source_type": "survey",
    "source_id": "my-first-survey",
    "field_id": "q1",
    "field_label": "How satisfied are you?",
    "field_type": "rating",
    "value_number": 5,
    "metadata": {
      "country": "US",
      "device": "desktop"
    }
  }'
```

:::info API Authentication
All API requests require the `X-API-Key` header with your configured `SERVICE_API_KEY`.
:::

Expected response:
```json
{
  "id": "01932c8a-8b9e-7000-8000-000000000001",
  "collected_at": "2025-10-20T12:34:56Z",
  "created_at": "2025-10-20T12:34:56Z",
  "source_type": "survey",
  "source_id": "my-first-survey",
  "field_id": "q1",
  "field_type": "rating",
  "value_number": 5,
  "metadata": {
    "country": "US",
    "device": "desktop"
  }
}
```

### 2. Create a Text Feedback Record (with AI Enrichment)

If you've configured OpenAI keys, text responses will be automatically enriched:

```bash
curl -X POST http://localhost:8080/v1/feedback-records \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your_secure_api_key_here" \
  -d '{
    "source_type": "survey",
    "source_id": "my-first-survey",
    "field_id": "q2",
    "field_label": "What can we improve?",
    "field_type": "text",
    "value_text": "The checkout process was confusing and took too long. Please simplify it!"
  }'
```

Hub will automatically:
- Extract sentiment (e.g., "negative")
- Detect emotion (e.g., "frustration")
- Identify topics (e.g., ["checkout", "user experience"])
- Generate embeddings for semantic search

### 3. Query Feedback Records

```bash
curl "http://localhost:8080/v1/feedback-records?limit=10" \
  -H "X-API-Key: your_secure_api_key_here"
```

Response:
```json
{
  "data": [...],
  "total": 2,
  "limit": 10,
  "offset": 0
}
```

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `SERVICE_DATABASE_URL` | ✅ Yes | - | PostgreSQL connection string |
| `SERVICE_API_KEY` | ✅ Yes | - | API authentication key |
| `SERVICE_PORT` | No | `8080` | HTTP server port |
| `SERVICE_HOST` | No | `0.0.0.0` | HTTP server host |
| `SERVICE_LOG_LEVEL` | No | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `SERVICE_ENVIRONMENT` | No | `production` | Environment name |
| `SERVICE_OPENAI_API_KEY` | No | - | OpenAI API key (required for AI features) |
| `SERVICE_OPENAI_ENRICHMENT_MODEL` | No | `gpt-4o-mini` | Model for sentiment/topic analysis |
| `SERVICE_OPENAI_EMBEDDING_MODEL` | No | `text-embedding-3-small` | Model for semantic search embeddings |
| `SERVICE_WEBHOOK_URLS` | No | - | Comma-separated webhook URLs |

:::info AI Features
If `SERVICE_OPENAI_API_KEY` is not provided, Hub will still work but text responses won't be enriched with sentiment/topic analysis or available for semantic search. [Learn more about AI enrichment →](./core-concepts/ai-enrichment)
:::

[See full environment variable reference →](./reference/environment-variables)

### Database Connection String Format

```
postgresql://username:password@host:port/database?sslmode=disable
```

For Docker Compose (services in same network):
```
postgresql://formbricks:password@postgres:5432/hub?sslmode=disable
```

For external PostgreSQL:
```
postgresql://user:pass@db.example.com:5432/hub?sslmode=require
```

## Managing the Service

### View Logs

```bash
# All services
docker-compose logs -f

# Just the Hub API
docker-compose logs -f hub

# Just PostgreSQL
docker-compose logs -f postgres
```

### Stop Services

```bash
docker-compose down
```

### Stop and Remove Data

:::danger Data Loss
This will delete all stored feedback records!
:::

```bash
docker-compose down -v
```

### Restart Services

```bash
docker-compose restart
```

### Update to Latest Version

```bash
docker-compose pull
docker-compose up -d
```

## Next Steps

<div className="row">
  <div className="col col--6 margin-bottom--lg">
    <div className="card">
      <div className="card__header">
        <h3>📚 Core Concepts</h3>
      </div>
      <div className="card__body">
        <ul>
          <li><a href="./core-concepts/data-model">Data Model</a></li>
          <li><a href="./core-concepts/ai-enrichment">AI Enrichment</a></li>
          <li><a href="./core-concepts/webhooks">Webhooks</a></li>
          <li><a href="./core-concepts/authentication">Authentication</a></li>
        </ul>
      </div>
    </div>
  </div>
  <div className="col col--6 margin-bottom--lg">
    <div className="card">
      <div className="card__header">
        <h3>🚀 Reference</h3>
      </div>
      <div className="card__body">
        <ul>
          <li><a href="./api-reference">API Reference</a></li>
          <li><a href="./reference/environment-variables">Environment Variables</a></li>
          <li><a href="./reference/architecture">Architecture</a></li>
        </ul>
      </div>
    </div>
  </div>
</div>

## Troubleshooting

### Port Already in Use

If port 8080 or 5432 is occupied:

```bash
# Change ports in .env
SERVICE_PORT=8081
```

Or modify the port mapping in `docker-compose.yml`:
```yaml
ports:
  - '8081:8080'  # Host:Container
```

### Container Fails to Start

Check the logs for errors:

```bash
docker-compose logs hub
```

Common issues:
- **Database not ready**: Wait for PostgreSQL healthcheck to pass
- **Invalid API key**: Ensure `SERVICE_API_KEY` is set in `.env`
- **Database connection failed**: Check `SERVICE_DATABASE_URL` format

### Database Connection Failed

Verify PostgreSQL is running:

```bash
docker-compose ps postgres
```

If unhealthy, check PostgreSQL logs:

```bash
docker-compose logs postgres
```

### Health Check Failing

Check if the service is listening:

```bash
docker exec formbricks_hub_api wget -O- http://localhost:8080/health
```

### Cannot Pull Docker Image

Ensure you have access to GitHub Container Registry:

```bash
docker pull ghcr.io/formbricks/hub:latest
```

If authentication is required:
```bash
echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin
```

### Data Not Persisting

Verify the volume is mounted:

```bash
docker volume ls | grep postgres_data
docker volume inspect formbricks-hub_postgres_data
```

## Getting Help

- **GitHub Discussions**: [Ask questions and share ideas](https://github.com/formbricks/hub/discussions)
- **GitHub Issues**: [Report bugs or request features](https://github.com/formbricks/hub/issues)
- **API Reference**: [Interactive documentation](./api-reference)
