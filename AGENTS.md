# Repository Guidelines

## Project Structure & Module Organization
- `cmd/api/` holds the API server (hub-api): HTTP API, ingestion, record retrieval, tenant/auth; enqueues jobs to River (insert-only). Build/run: `go run ./cmd/api` or `make run`.
- `cmd/worker/` holds the worker (hub-worker): runs River job workers (webhook delivery, embeddings). No HTTP. Build/run: `go run ./cmd/worker` or `make run-worker`.
- `internal/` contains core application layers: `api/handlers`, `api/middleware`, `service`, `repository`, `models`, `config`, and `workers`.
- `pkg/` provides shared utilities (currently `pkg/database`).
- `migrations/` stores SQL migration files (goose); use `-- +goose up` / `-- +goose down` annotations.
- `tests/` contains integration tests.

## Build, Test, and Development Commands
- `make dev-setup`: start Postgres via Docker, install Go deps/tools, and initialize database schema.
- `make run`: run hub-api (config from `.env` if present and environment variables; copy `.env.example` to `.env` or set env vars).
- `make run-worker`: run hub-worker (requires `.env` with DATABASE_URL).
- `make build`: build both `bin/hub-api` and `bin/hub-worker`. Use `make build-api` or `make build-worker` for a single binary.
- `make tests`: run integration tests in `tests/`.
- `make tests-coverage`: generate `coverage.html`.
- `make check-coverage`: run all tests with coverage and fail if below COVERAGE_THRESHOLD (excludes cmd/api and cmd/worker main packages).
- `make init-db`: run goose migrations up using `DATABASE_URL`. `make migrate-status` and `make migrate-validate` for status and validation. New migrations go in `migrations/` with goose annotations (`-- +goose up` / `-- +goose down`). Name files with a sequential number and short description (e.g. `002_add_webhooks_table.sql`); goose orders by the numeric prefix. For webhook delivery, run `make river-migrate` after `init-db` to apply River job queue migrations.
- `make fmt`: format code (runs `golangci-lint run --fix`; uses gofumpt/gci from config).
- `make lint`: run `golangci-lint` (includes format checks; requires `make install-tools`).

## Coding Style & Naming Conventions
- Language: Go; format with `make fmt` (golangci-lint applies gofumpt/gci).
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
