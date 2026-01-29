# Formbricks Hub

An open-source Experience Management (XM) database service. Hub is a headless API service that unifies customer feedback from multiple sources into a single, normalized data model optimized for analytics and AI-powered insights.

## What is Hub?

**Hub** is a specialized Experience Management database that:

- **Connects all feedback sources** (surveys, reviews, support tickets, social media, etc.)
- **Auto-applies taxonomy and mapping** with AI-powered context & enrichment
- **Provides a unified data model** optimized for human and AI analytics
- **Sends all events via webhooks** (Standard Webhooks specification)
- **Headless/API-only design** - UI lives in XM Suite (Hub is the backend)

**Key Design Principles**:
- **Performance First**: Optimized for high-throughput data ingestion and retrieval
- **Data Processing**: Focus on query performance, indexing strategies, and analytics capabilities
- **Simple Architecture**: Barebones implementation with raw `net/http` and `pgx` for maintainability
- **Headless Design**: API-only service (no UI)
- **Webhook-Only Communication**: Events sent via Standard Webhooks (no workflow engine)
- **Enterprise Scale**: Designed to handle hundreds of millions of records per day

## Features

### Current Features

- ✅ **RESTful API** for feedback record CRUD operations
- ✅ **PostgreSQL** with pgvector for data persistence and vector search
- ✅ **AI-Powered Taxonomy** - automatic topic clustering with UMAP, HDBSCAN, and GPT-4o
- ✅ **Embedding Generation** - async embedding generation via River job queue
- ✅ **API Key Authentication** via environment variable
- ✅ **Clean Architecture** with repository, service, and handler layers
- ✅ **Docker Compose** for local development and production
- ✅ **Swagger/OpenAPI** documentation
- ✅ **Health Check** endpoints

## Tech Stack

- **Language**: Go 1.25.6
- **Database**: PostgreSQL 16
- **Driver**: pgx/v5
- **HTTP**: Standard library `net/http` (barebones approach)
- **Documentation**: Swagger/OpenAPI
- **License**: Apache 2.0

## Project Structure

```
.
├── cmd/
│   ├── api/              # API server entrypoint
│   └── backfill/         # CLI tool for backfilling embeddings
├── internal/
│   ├── api/
│   │   ├── handlers/     # HTTP request handlers
│   │   └── middleware/   # HTTP middleware (auth, CORS, logging)
│   ├── service/          # Business logic layer
│   ├── repository/       # Data access layer
│   ├── models/           # Domain models and DTOs
│   ├── worker/           # Background workers (taxonomy scheduler)
│   ├── jobs/             # River job queue workers
│   └── config/           # Configuration management
├── pkg/
│   └── database/         # Database utilities and connection pooling
├── services/
│   └── taxonomy-generator/  # Python microservice for ML clustering
├── sql/                  # SQL schema files
├── tests/                # Integration tests
└── docs/                 # API documentation (Swagger)
```

## Getting Started

### Prerequisites

- Go 1.25.6 or higher
- Docker and Docker Compose
- Make (optional, for convenience)

### Quick Start

1. Clone the repository and navigate to the project directory

2. Set up the development environment:
```bash
make dev-setup
```

This will:
- Start PostgreSQL container
- Install Go dependencies
- Install development tools (gofumpt, golangci-lint)
- Initialize database schema
- Install git hooks for code quality

3. Start the API server:
```bash
make run
```

The server will start on `http://localhost:8080`

### Manual Setup

If you prefer not to use Make:

1. Start Docker containers:
```bash
docker compose up -d
```

2. Copy environment variables:
```bash
cp .env.example .env
```

3. Initialize database schema:
```bash
make init-db
```

4. Set your API key:
```bash
export API_KEY=your-secret-key-here
```

Or add it to your `.env` file:
```
API_KEY=your-secret-key-here
```

5. Start the server:
```bash
go run ./cmd/api/main.go
```

## API Endpoints

### Health Check
- `GET /health` - Health check endpoint
- `GET /swagger/` - Swagger API documentation

### Feedback Records

#### Create Feedback Record
```bash
POST /v1/feedback-records
Authorization: Bearer <api-key>
Content-Type: application/json

{
  "source_type": "survey",
  "source_id": "survey-123",
  "source_name": "Customer Feedback Survey",
  "field_id": "question-1",
  "field_label": "How satisfied are you?",
  "field_type": "rating",
  "value_number": 5,
  "metadata": {"campaign": "summer-2025"},
  "language": "en",
  "user_identifier": "user-abc"
}
```

#### Get Feedback Record by ID
```bash
GET /v1/feedback-records/{id}
Authorization: Bearer <api-key>
```

#### List Feedback Records
```bash
GET /v1/feedback-records?source_type=survey&limit=50&offset=0
Authorization: Bearer <api-key>
```

Query parameters:
- `source_type` - Filter by source type
- `source_id` - Filter by source ID
- `field_id` - Filter by field ID
- `user_identifier` - Filter by user identifier
- `limit` - Number of results (default: 100, max: 1000)
- `offset` - Pagination offset

#### Update Feedback Record
```bash
PATCH /v1/feedback-records/{id}
Authorization: Bearer <api-key>
Content-Type: application/json

{
  "value_number": 4,
  "metadata": {"updated": true}
}
```

#### Delete Feedback Record
```bash
DELETE /v1/feedback-records/{id}
Authorization: Bearer <api-key>
```

## Development

### Available Make Commands

```bash
make help           # Show all available commands

# Development
make dev-setup      # Set up dev environment (postgres, deps, tools, schema, hooks)
make run            # Run Go API server locally (port 8080)
make taxonomy-dev   # Run Python taxonomy service locally (port 8001)
make docker-up      # Start dev infrastructure (postgres, pgadmin)
make docker-down    # Stop dev infrastructure

# Production
make prod-up        # Start full containerized stack
make prod-down      # Stop full stack
make prod-logs      # View production logs

# Build & Test
make build          # Build all binaries
make test-unit      # Fast unit tests (no database)
make tests          # Integration tests (requires database)
make test-all       # Run all tests
```

### Running Tests

```bash
make test-unit    # Fast unit tests (no database)
make tests        # Integration tests (requires database)
make test-all     # Run all tests
```

### Running with Taxonomy Service

For full AI-powered topic clustering, run both services:

```bash
# Terminal 1: Go API
make run

# Terminal 2: Python taxonomy service
make taxonomy-dev
```

The taxonomy service requires `OPENAI_API_KEY` in `services/taxonomy-generator/.env`.

### Git Hooks

The repository includes pre-commit hooks for code quality. To install them:

```bash
make install-hooks
```

The pre-commit hook automatically:
1. Checks for potential secrets in staged changes (warning only)
2. Formats staged Go files with `gofumpt`
3. Runs the linter (`golangci-lint`)

If formatting or linting fails, the commit is blocked.

**Note:** Git hooks are automatically installed when you run `make dev-setup`.

To bypass hooks temporarily (use sparingly):
```bash
git commit --no-verify -m "WIP: work in progress"
```

### Database Schema

The database schema is stored in the `sql/` directory.

To initialize the database schema:
```bash
make init-db
```

This will execute `sql/001_initial_schema.sql` using `psql`. Make sure you have `DATABASE_URL` set in your environment or `.env` file.

### API Key Authentication

Authentication is done via a single API key set in the `API_KEY` environment variable. The API key is validated against this environment variable for all protected endpoints.

Set your API key:
```bash
export API_KEY=your-secret-key-here
```

Or add it to your `.env` file:
```
API_KEY=your-secret-key-here
```

All protected endpoints require the `Authorization: Bearer <key>` header with the API key matching the `API_KEY` environment variable.

**Important**: The `API_KEY` environment variable is **required**. The server will fail to start if `API_KEY` is not set.

## Environment Variables

See [.env.example](.env.example) for all available configuration options:

- `DATABASE_URL` - PostgreSQL connection string
- `PORT` - HTTP server port (default: 8080)
- `API_KEY` - API key for authentication (required for protected endpoints)
- `ENV` - Environment (development/production)
- `LOG_LEVEL` - Logging level (debug, info, warn, error)

## Example Requests

### Create a text response
```bash
curl -X POST http://localhost:8080/v1/feedback-records \
  -H "Authorization: Bearer <your-api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "source_type": "form",
    "field_id": "email",
    "field_type": "text",
    "value_text": "user@example.com"
  }'
```

### Get all feedback records
```bash
curl http://localhost:8080/v1/feedback-records \
  -H "Authorization: Bearer <your-api-key>"
```

### Update a feedback record
```bash
curl -X PATCH http://localhost:8080/v1/feedback-records/{id} \
  -H "Authorization: Bearer <your-api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "metadata": {"verified": true}
  }'
```

### Delete a feedback record
```bash
curl -X DELETE http://localhost:8080/v1/feedback-records/{id} \
  -H "Authorization: Bearer <your-api-key>"
```

## Architecture

The application follows a clean architecture pattern:

1. **Handlers** - Handle HTTP requests and responses
2. **Service** - Contain business logic and validation
3. **Repository** - Handle data access and queries
4. **Models** - Define domain entities and DTOs

This separation allows for:
- Easy testing and mocking
- Clear separation of concerns
- Simple maintenance and refactoring

### Design Decisions

- **Barebones HTTP**: Using raw `net/http` instead of frameworks for simplicity and maintainability
- **Raw SQL**: Using `pgx` with raw SQL queries instead of ORMs for performance and control
- **Headless Design**: API-only service - no UI components
- **Webhook Communication**: Events sent via Standard Webhooks (no workflow engine)
- **Performance First**: Optimized for high-throughput data processing and analytics

## Contributing

This is an open-source project. Contributions are welcome! Please ensure:

- Code follows Go best practices
- Tests are included for new features
- Documentation is updated
- Changes align with the architecture principles

## License

Apache 2.0

## Related Documentation

- [API Documentation](http://localhost:8080/swagger/) - Interactive Swagger documentation (when server is running)