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
- ✅ **PostgreSQL** for data persistence with optimized schema
- ✅ **API Key Authentication** via environment variable
- ✅ **Clean Architecture** with repository, service, and handler layers
- ✅ **Docker Compose** for local development
- ✅ **Database Schema** initialization
- ✅ **OpenAPI** spec (`openapi.yaml`) and contract tests (Schemathesis)
- ✅ **Health Check** endpoints

## Tech Stack

- **Language**: Go 1.25.7
- **Database**: PostgreSQL 16
- **Driver**: pgx/v5
- **HTTP**: Standard library `net/http` (barebones approach)
- **Documentation**: OpenAPI spec in repo, contract tests (Schemathesis)
- **License**: Apache 2.0

## Project Structure

```
.
├── cmd/
│   └── api/              # API server entrypoint
├── internal/
│   ├── api/
│   │   ├── handlers/     # HTTP request handlers
│   │   └── middleware/   # HTTP middleware (auth, CORS, logging)
│   ├── service/          # Business logic layer
│   ├── repository/       # Data access layer
│   ├── models/           # Domain models and DTOs
│   └── config/           # Configuration management
├── pkg/
│   └── database/         # Database utilities and connection pooling
├── migrations/           # SQL migrations (goose)
├── tests/               # Integration tests
└── docs/                # API and product documentation
```

## Getting Started

### Prerequisites

- **Go 1.25.7 or higher** — must be [installed](https://go.dev/doc/install) and available on your `PATH`. Verify with `go version`. If `go` is not found, see [Adding Go to PATH](#adding-go-to-path) below.
- **Docker** and **Docker Compose**
- **Make** (optional, for convenience)

### Quick Start

1. Clone the repository and navigate to the project directory

2. Set up the development environment:
```bash
make dev-setup
```

This will:
- Start PostgreSQL container
- Install Go dependencies
- Install development tools (golangci-lint, govulncheck, goose)
- Run database migrations (goose)
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
go run ./cmd/api
```

## API Endpoints

### Health Check
- `GET /health` - Health check endpoint

The OpenAPI 3.1 spec lives in **`openapi.yaml`** at the repo root and is used by Schemathesis for API contract tests (`make schemathesis`). Serving the spec (e.g. `GET /openapi.json` or Swagger UI) is planned; see [todo.md](todo.md) § OpenAPI.

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
make help         # Show all available commands
make dev-setup    # Set up development environment (docker, deps, tools, schema)
make build        # Build all binaries
make run          # Run the API server
make tests        # Run all tests
make init-db      # Run migrations (goose up)
make migrate-status   # Show migration status
make migrate-validate # Validate migration files (no DB)
make docker-up    # Start Docker containers
make docker-down  # Stop Docker containers
make clean        # Clean build artifacts
```

### Running Tests

```bash
make test-unit    # Fast unit tests (no database)
make tests        # Integration tests (requires database)
make test-all     # Run all tests
```

### Troubleshooting (local development)

- **`go: command not found` or `make` fails with "go not found"** — Go is not on your `PATH`. Install Go from [go.dev/doc/install](https://go.dev/doc/install), then add it to your PATH using the platform steps in [Adding Go to PATH](#adding-go-to-path) below. Restart the terminal (or re-source your profile) after changing PATH.
- **Docker/Postgres port already in use** (e.g. `5432` or `address already in use`) — Set `POSTGRES_PORT` in `.env` to a free port (e.g. `5433`), and set the same port in `DATABASE_URL` (e.g. `postgres://postgres:postgres@localhost:5433/test_db?sslmode=disable`). Then run `make docker-up` again.

#### Adding Go to PATH

After installing Go, your shell must be able to find the `go` binary. Restart the terminal (or re-source your profile) after editing PATH.

**macOS / Linux (bash or zsh)**  
Add this line to `~/.bashrc` or `~/.zshrc` (create the file if it doesn’t exist). It adds the Go toolchain (`go`) and the directory where `go install` puts binaries (e.g. `goose`, `river`) used by the Makefile.

```bash
export PATH="$PATH:/usr/local/go/bin:$HOME/go/bin"
```

If you installed Go via the [Go version manager](https://go.dev/doc/manage-install) (e.g. under `$HOME/sdk/go1.21/bin`), use that directory instead of `/usr/local/go/bin`. To check that Go is on your PATH, run `which go` — it should print the path to the `go` binary.

### Git Hooks

The repository includes pre-commit hooks for code quality. To install them:

```bash
make install-hooks
```

The pre-commit hook automatically:
1. Validates migration files when `migrations/` is changed
2. Runs `golangci-lint run --fix` (format and fixable issues), then the linter (includes gosec for secrets in Go code)

If any check fails, the commit is blocked.

**Note:** Git hooks are automatically installed when you run `make dev-setup`.

To bypass hooks temporarily (use sparingly):
```bash
git commit --no-verify -m "WIP: work in progress"
```

### Migrations

Schema is managed with [goose](https://github.com/pressly/goose). Migrations live in `migrations/`.

- **Apply migrations**: `make init-db` (runs `goose up`; requires `DATABASE_URL` and goose on PATH — install via `make install-tools`).
- **Check status**: `make migrate-status`.
- **Validate files** (no DB): `make migrate-validate` (use in CI before applying).

In production, run migrations as a separate deploy step before deploying new app code (not on app startup). To add a new migration, create a file in `migrations/` with the next version prefix (e.g. `002_add_foo.sql`) and add `-- +goose up` and optionally `-- +goose down` sections; one logical change per file.

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

Copy [.env.example](.env.example) to `.env` and set values as needed. Reference:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `API_KEY` | Yes | — | API key for authenticating requests to protected endpoints. Server will not start without it. |
| `DATABASE_URL` | No | `postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable` | PostgreSQL connection string. Format: `postgres://user:password@host:port/database?sslmode=...`. When using Docker Compose, the host port in this URL must match `POSTGRES_PORT`. |
| `POSTGRES_PORT` | No | `5432` | Host port used by Docker Compose for the Postgres container (`host:container` = `POSTGRES_PORT:5432`). Set this (e.g. `5433`) only if 5432 is already in use; set the same port in `DATABASE_URL`. |
| `PORT` | No | `8080` | HTTP server listen port. |
| `LOG_LEVEL` | No | `info` | Log level: `debug`, `info`, `warn`, or `error`. |

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

- [OpenAPI spec](openapi.yaml) - API contract (used by Schemathesis); serving it from the server is planned (see [todo.md](todo.md)).