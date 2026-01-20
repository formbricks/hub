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
- ✅ **API Key Authentication** with secure key management
- ✅ **Clean Architecture** with repository, service, and handler layers
- ✅ **Docker Compose** for local development
- ✅ **Database Migrations** with version tracking
- ✅ **Swagger/OpenAPI** documentation
- ✅ **Health Check** endpoints

### Planned Features (See Implementation Plan)

The project follows a comprehensive [feature parity and production readiness plan](.cursor/plans/feature_parity_implementation_plan_766455a7.plan.md) organized by priority:

**Priority 1: Critical Security & Production Readiness**
- Rate limiting
- Request size limits
- Error sanitization
- API key security enhancements

**Priority 2: Core Features**
- API specification alignment (`/v1/feedback-records` endpoint)
- Webhook system (Standard Webhooks)
- Bulk delete (GDPR compliance)
- Multi-tenancy support
- Enhanced database indexing

**Priority 3-7: Observability, Operations & Advanced Features**
- Structured logging with request ID tracking
- Prometheus metrics
- OpenTelemetry integration
- Health check enhancements
- Configuration validation
- Security headers & CORS
- And more...

**Future: AI-Powered Connector Architecture**
- AI-powered connector discovery and mapping
- Multiple ingestion methods (file imports, streaming, webhooks)
- Semantic data context understanding
- Automatic field mapping and taxonomy application

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
│   ├── migrate/          # Migration runner
│   └── createkey/        # API key generation utility
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
├── migrations/           # SQL migrations
├── tests/               # Integration tests
└── docs/                # API documentation (Swagger)
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
make setup
```

This will:
- Start PostgreSQL container
- Create a `.env` file from `.env.example`
- Run database migrations

3. Generate an API key:
```bash
make create-key
# or
go run ./cmd/createkey/main.go
```

4. Start the API server:
```bash
make run
```

The server will start on `http://localhost:8080`

### Manual Setup

If you prefer not to use Make:

1. Start Docker containers:
```bash
docker-compose up -d
```

2. Copy environment variables:
```bash
cp .env.example .env
```

3. Run migrations:
```bash
go run ./cmd/migrate/main.go
```

4. Generate an API key:
```bash
go run ./cmd/createkey/main.go
```

5. Start the server:
```bash
go run ./cmd/api/main.go
```

## API Endpoints

### Health Check
- `GET /health` - Health check endpoint
- `GET /swagger/` - Swagger API documentation

### Feedback Records (Experiences)

**Note**: The API currently uses `/v1/experiences` endpoints. According to the implementation plan, these will be migrated to `/v1/feedback-records`.

#### Create Feedback Record
```bash
POST /v1/experiences
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
GET /v1/experiences/{id}
Authorization: Bearer <api-key>
```

#### List Feedback Records
```bash
GET /v1/experiences?source_type=survey&limit=50&offset=0
Authorization: Bearer <api-key>
```

Query parameters:
- `source_type` - Filter by source type
- `source_id` - Filter by source ID
- `field_id` - Filter by field ID
- `user_identifier` - Filter by user identifier
- `limit` - Number of results (default: 100, max: 1000)
- `offset` - Pagination offset

#### Search Feedback Records
```bash
GET /v1/experiences/search?query=feedback&source_type=survey&pageSize=20&page=0
Authorization: Bearer <api-key>
```

#### Update Feedback Record
```bash
PATCH /v1/experiences/{id}
Authorization: Bearer <api-key>
Content-Type: application/json

{
  "value_number": 4,
  "metadata": {"updated": true}
}
```

#### Delete Feedback Record
```bash
DELETE /v1/experiences/{id}
Authorization: Bearer <api-key>
```

## Development

### Available Make Commands

```bash
make help         # Show all available commands
make build        # Build all binaries
make run          # Run the API server
make test         # Run tests
make migrate      # Run database migrations
make create-key   # Generate a new API key
make docker-up    # Start Docker containers
make docker-down  # Stop Docker containers
make clean        # Clean build artifacts
```

### Running Tests

```bash
make test
```

### Database Migrations

Migrations are stored in the `migrations/` directory and are executed in alphabetical order.

To run migrations:
```bash
make migrate
```

Or manually:
```bash
go run ./cmd/migrate/main.go
```

### API Key Management

Generate a new API key:
```bash
make create-key
# or
go run ./cmd/createkey/main.go
```

API keys are stored in the `api_keys` table and are used for authentication via the `Authorization: Bearer <key>` header.

## Environment Variables

See [.env.example](.env.example) for all available configuration options:

- `DATABASE_URL` - PostgreSQL connection string
- `PORT` - HTTP server port (default: 8080)
- `ENV` - Environment (development/production)
- `LOG_LEVEL` - Logging level (debug, info, warn, error)

## Example Requests

### Create a text response
```bash
curl -X POST http://localhost:8080/v1/experiences \
  -H "Authorization: Bearer <your-api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "source_type": "form",
    "field_id": "email",
    "field_type": "text",
    "value_text": "user@example.com"
  }'
```

### Get all experiences
```bash
curl http://localhost:8080/v1/experiences \
  -H "Authorization: Bearer <your-api-key>"
```

### Update an experience
```bash
curl -X PATCH http://localhost:8080/v1/experiences/{id} \
  -H "Authorization: Bearer <your-api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "metadata": {"verified": true}
  }'
```

### Delete an experience
```bash
curl -X DELETE http://localhost:8080/v1/experiences/{id} \
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

## Roadmap

See the [comprehensive implementation plan](.cursor/plans/feature_parity_implementation_plan_766455a7.plan.md) for detailed roadmap and priorities.

**Current Focus**:
- Security & production readiness improvements
- API specification alignment
- Core feature implementation (webhooks, bulk operations, multi-tenancy)

**Future Enhancements**:
- AI-powered connector framework
- Semantic search capabilities
- AI enrichment (sentiment, emotion, topics)
- Advanced observability and monitoring

## Contributing

This is an open-source project. Contributions are welcome! Please ensure:

- Code follows Go best practices
- Tests are included for new features
- Documentation is updated
- Changes align with the architecture principles

## License

Apache 2.0

## Related Documentation

- [Implementation Plan](.cursor/plans/feature_parity_implementation_plan_766455a7.plan.md) - Comprehensive feature parity and production readiness plan
- [API Documentation](http://localhost:8080/swagger/) - Interactive Swagger documentation (when server is running)
- [Agents Documentation](agents.md) - AI agents and assistants used in the project
