# Repository Guidelines

## Project Structure & Module Organization
- `cmd/api/` holds the API server entrypoint (`main.go`).
- `internal/` contains core application layers: `api/handlers`, `api/middleware`, `service`, `repository`, `models`, `worker`, and `config`.
- `pkg/` provides shared utilities (currently `pkg/database`).
- `sql/` stores SQL schema files (e.g., `sql/001_initial_schema.sql`).
- `services/` contains microservices (e.g., `services/taxonomy-generator/` Python service).
- `tests/` contains integration tests.

## Build, Test, and Development Commands
- `make dev-setup`: start Postgres via Docker, install Go deps/tools, and initialize database schema.
- `make run`: create a default `.env` if missing and run the API server.
- `make build`: build the API binary to `bin/api`.
- `make tests`: run integration tests in `tests/`.
- `make tests-coverage`: generate `coverage.html`.
- `make init-db`: initialize database schema using `DATABASE_URL`.
- `make fmt` / `make fmt-check`: format or verify formatting with `gofumpt`.
- `make lint`: run `golangci-lint` (requires `make install-tools`).

## Coding Style & Naming Conventions
- Language: Go; format with `gofumpt` (run `make fmt`).
- Prefer Go naming conventions (CamelCase for exported, lowerCamel for unexported).
- Keep package names short and domain-focused (e.g., `repository`, `service`).

## Testing Guidelines
- Tests live under `tests/` and are run with `go test ./tests/...`.
- Name test files `*_test.go` and test functions `TestXxx`.
- Use `make tests-coverage` when adding meaningful logic to track coverage output.

## Commit & Pull Request Guidelines
- Always create a pull request for changes and don't commit directly to main.
- Commit messages follow a Conventional Commits style (e.g., `chore: fix linting errors`).
- PRs should describe the change, include test evidence (command + result), and link related issues when available.
- For API behavior changes, include request/response examples or screenshots of Swagger UI.

## Security & Configuration Tips
- Configure `API_KEY` and `DATABASE_URL` via `.env` or environment variables.
- Do not commit `.env` or secrets; use `.env.example` as the base.

## Taxonomy Service Architecture
The taxonomy feature uses a Python microservice for ML clustering:

- **Go API** triggers jobs via HTTP to the taxonomy-generator service
- **Python service** writes results directly to Postgres (topics table, feedback_records.topic_id)
- **TaxonomyScheduler** (`internal/worker/`) polls for scheduled jobs and tracks completion
- Config: `TAXONOMY_SERVICE_URL`, `TAXONOMY_SCHEDULER_ENABLED`, `TAXONOMY_POLL_INTERVAL`

To run the taxonomy service:
```bash
cd services/taxonomy-generator
pip install -r requirements.txt
uvicorn src.main:app --port 8001
```

Key endpoints: `POST /v1/taxonomy/{tenant_id}/generate`, `GET /v1/taxonomy/{tenant_id}/status`
