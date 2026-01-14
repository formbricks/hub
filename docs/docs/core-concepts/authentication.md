---
sidebar_position: 2
---

# Authentication

Secure your Hub API with optional API key authentication. Enable it in production with a single environment variable.

## How It Works

Hub runs **without authentication by default** for easy local development. When you set `SERVICE_API_KEY`, all API endpoints require the `X-API-Key` header - except health checks and documentation, which remain public.

## Enabling API Key Authentication

### 1. Set Environment Variable

Add `SERVICE_API_KEY` to your `.env` file:

```bash
SERVICE_API_KEY=your-secret-key-here
```

:::tip Generating Secure Keys
Generate a cryptographically secure key:

```bash
# macOS/Linux
openssl rand -base64 32

# Or use a password manager's random generator
```
:::

### 2. Restart the Service

```bash
# Stop the service (Ctrl+C)
# Then restart:
make dev
```

You should see in the logs:
```
ℹ️  API key authentication enabled
```

### 3. Include Key in Requests

All API requests (except `/health` and `/docs`) now require the `X-API-Key` header:

```bash
curl -H "X-API-Key: your-secret-key-here" \
  http://localhost:8080/v1/experiences
```

## Protected Endpoints

When API key auth is enabled, these endpoints require authentication:

- `POST /v1/experiences` - Create experience
- `GET /v1/experiences` - List experiences
- `GET /v1/experiences/{id}` - Get experience
- `PATCH /v1/experiences/{id}` - Update experience
- `DELETE /v1/experiences/{id}` - Delete experience
- `GET /v1/experiences/search` - Semantic search

**Always public** (no auth required):
- `GET /health` - Health check
- `GET /docs` - API documentation
- `GET /openapi.json` - OpenAPI spec

## Authentication Workflow

### Request Flow

```mermaid
graph TD
    A[Client Request] --> B{Has X-API-Key?}
    B -->|No| C[401 Unauthorized]
    B -->|Yes| D{Key Matches?}
    D -->|No| E[401 Unauthorized]
    D -->|Yes| F[Process Request]
    F --> G[200 Success]
```

### Example Requests

**Without authentication (enabled):**
```bash
curl http://localhost:8080/v1/experiences
```

Response:
```json
{
  "title": "Unauthorized",
  "status": 401,
  "detail": "Missing or invalid API key"
}
```

**With authentication:**
```bash
curl -H "X-API-Key: your-secret-key-here" \
  http://localhost:8080/v1/experiences
```

Response:
```json
{
  "data": [...],
  "total": 10,
  ...
}
```

## Production Deployment

Hub's built-in API key authentication is ideal for:
- ✅ Internal services and tools
- ✅ Server-to-server communication
- ✅ Quick production deployments

For public APIs or multi-tenant systems, consider deploying behind an API gateway (AWS API Gateway, Kong, Traefik) for advanced features like OAuth, JWT, or per-user access control.

### Docker Deployment with Auth

```bash
docker run -d \
  -p 8080:8080 \
  -e SERVICE_DATABASE_URL=postgres://... \
  -e SERVICE_API_KEY=your-secret-key-here \
  ghcr.io/formbricks/hub:latest
```

### Environment Variables

```bash
# Required
SERVICE_DATABASE_URL=postgres://user:pass@host/db

# Optional (but recommended for production)
SERVICE_API_KEY=your-secret-key-here
SERVICE_LOG_LEVEL=info
SERVICE_ENVIRONMENT=production
```

[See full environment variable reference →](../reference/environment-variables)

## Security Best Practices

### 1. Use HTTPS in Production

Always use HTTPS to encrypt API keys in transit:

```
❌ http://api.example.com  (insecure)
✅ https://api.example.com (secure)
```

### 2. Store Keys Securely

**✅ Good practices:**
- Use environment variables (not hardcoded)
- Store in secret management (AWS Secrets Manager, HashiCorp Vault)
- Rotate keys regularly

**❌ Avoid:**
- Committing keys to Git
- Sharing keys via email/Slack
- Using the same key for dev and prod

### 3. Rotate Keys Regularly

Schedule regular key rotation (e.g., every 90 days):

1. Generate new key
2. Update `SERVICE_API_KEY` in production
3. Restart service
4. Update clients with new key
5. Revoke old key

### 4. Monitor for Unauthorized Access

Enable structured logging to track failed auth attempts:

```bash
SERVICE_LOG_LEVEL=info
```

Look for 401 responses in logs:

```json
{
  "level": "warn",
  "msg": "unauthorized request",
  "path": "/v1/experiences",
  "ip": "203.0.113.1"
}
```

### 5. Rate Limiting

Hub includes built-in rate limiting to protect against abuse:

- **Per-IP limits**: Default 100 requests/second per IP address
- **Global limits**: Default 1000 requests/second across all IPs
- **Configurable**: Adjust via `SERVICE_RATE_LIMIT_*` environment variables

[Learn more about rate limiting configuration →](../reference/environment-variables#rate-limiting)

## Disabling Authentication

To disable auth for local development:

1. Remove or comment out `SERVICE_API_KEY` in `.env`:
   ```bash
   # SERVICE_API_KEY=your-key
   ```

2. Restart:
   ```bash
   make dev
   ```

Logs will show:
```
ℹ️  API key authentication disabled
```

## Troubleshooting

### 401 Unauthorized Error

**Symptom:**
```json
{
  "title": "Unauthorized",
  "status": 401,
  "detail": "Missing or invalid API key"
}
```

**Solutions:**

1. **Check if auth is enabled:**
   ```bash
   grep SERVICE_API_KEY .env
   ```

2. **Verify header syntax:**
   ```bash
   # Correct
   curl -H "X-API-Key: your-key" http://...
   
   # Wrong (lowercase 'k')
   curl -H "X-API-key: your-key" http://...
   ```

3. **Check key matches `.env`:**
   ```bash
   echo $SERVICE_API_KEY
   ```

### Health Check Returns 401

Health checks (`/health`) should **never** require auth.

If you see this, it's a bug. Please [report it](https://github.com/formbricks/hub/issues).

### Docs Page Returns 401

The `/docs` endpoint should always be public.

If you see this, it's a bug. Please [report it](https://github.com/formbricks/hub/issues).

## Examples

### cURL

```bash
curl -H "X-API-Key: abc123" \
  -X POST http://localhost:8080/v1/experiences \
  -H "Content-Type: application/json" \
  -d '{"source_type":"survey",...}'
```

### JavaScript (Node.js)

```javascript
const response = await fetch('http://localhost:8080/v1/experiences', {
  headers: {
    'X-API-Key': 'abc123',
    'Content-Type': 'application/json',
  },
  method: 'POST',
  body: JSON.stringify({
    source_type: 'survey',
    field_id: 'q1',
    field_type: 'text',
    value_text: 'Great!',
  }),
});
```

### Python

```python
import requests

headers = {
    'X-API-Key': 'abc123',
    'Content-Type': 'application/json',
}

response = requests.get(
    'http://localhost:8080/v1/experiences',
    headers=headers
)
```

### Go

```go
client := &http.Client{}
req, _ := http.NewRequest("GET", "http://localhost:8080/v1/experiences", nil)
req.Header.Set("X-API-Key", "abc123")

resp, err := client.Do(req)
```

## Next Steps

- [Webhooks](./webhooks) - React to data changes
- [API Reference](../api-reference) - Explore endpoints
- [Environment Variables](../reference/environment-variables) - Configuration reference

