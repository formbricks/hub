# Repository Guidelines

## Project Structure & Module Organization
- `cmd/api/` holds the API server (hub-api): HTTP API, ingestion, record retrieval, tenant/auth, semantic search; enqueues jobs to River (insert-only). Build/run: `go run ./cmd/api` or `make run`.
- `cmd/worker/` holds the worker (hub-worker): runs River job workers — webhook delivery and the enrichment pipelines (embeddings, translation, sentiment, emotions). No HTTP. Build/run: `go run ./cmd/worker` or `make run-worker`.
- `cmd/backfill-*/` are one-off enqueue commands that (re)enrich an existing backlog: `backfill-embeddings`, `backfill-translations`, and `backfill-classify -type sentiment|emotions`. hub-worker processes the jobs they enqueue.
- `internal/` contains the application layers: `api/handlers`, `api/middleware`, `service`, `repository`, `models`, `config`, `workers`, `observability` (OTel metrics/tracing), the LLM seam (`llm`, `openai`, `googleai`), `datatypes`, and `huberrors`.
- `pkg/` provides shared utilities: `database`, `cursor` (keyset pagination), and `embeddings`.
- `migrations/` stores SQL migration files (goose); use `-- +goose up` / `-- +goose down` annotations.
- `tests/` contains integration tests (they require a pgvector database — see Testing Guidelines).

## Build, Test, and Development Commands
- `make dev-setup`: start Postgres via Docker, install Go deps/tools, and initialize database schema.
- `make run`: apply River migrations, then run both hub-api and hub-worker (config from `.env` if present, else environment variables; copy `.env.example` to `.env` or set env vars).
- `make run-api` / `make run-worker`: run a single process (worker requires DATABASE_URL from `.env` or environment variables).
- `make run-backfill-embeddings` / `make run-backfill-translations` / `make run-backfill-classify TYPE=sentiment|emotions`: enqueue a one-off (re)enrichment of the existing backlog (loads `.env`). `make build-backfill-*` build the corresponding binaries.
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

## Enrichment Framework

Sentiment, emotions, and translation are "classify" enrichments (`text → structured result` via an LLM) that share one scaffold; embedding is a separate pipeline that reuses only the light shared layer (client registry, metrics, content-hash). **Do not copy an existing enrichment to add a new one** — reuse the shared pieces:

- `internal/service/enrichment_client_factory.go` — provider registry + validation and the shared `EnrichmentClientConfig` (per-type configs are aliases of it).
- `internal/service/enrichment_client.go` — `classifyStructured` + `structuredSpec[R]` (prompt/schema/parse); the single place that calls the provider.
- `internal/service/enrichment_provider.go` — `EnrichmentProvider` (eligibility, per-type `triggers`, per-tenant gate, dedupe, `buildArgs`).
- `internal/workers/enrichment_worker.go` — the generic `enrichmentWorker[A, R]` Work body (load → clear-on-empty or classify → persist, with all outcome/error mapping).
- `internal/observability/enrichment_metrics.go` — one metrics impl behind the per-type interfaces.

A new enrichment is then a thin per-type spec: an enum/result type in `models`, a `*_client.go` (prompt + parse + `structuredSpec`), a `*_provider.go` (config for `EnrichmentProvider`), a `*_job_args.go`, a one-line `*_worker.go` (a configured `enrichmentWorker`), a metrics adapter, a config struct, and wiring in `cmd/*`. Enrichment outputs are read-only, server-generated columns — `NULL` until enriched, cleared and re-enqueued when `value_text` changes (translation also on `language`). Writes are guarded against content supersession, so a job that read older text skips rather than overwriting a newer result; carry `tenant_id` (and, for correlation, the originating `event_id`) through job args and log lines.

## Multi-Tenancy & Data Isolation

- Treat `tenant_id` as a security boundary, not a convenience filter. Tenant-owned data must never be read, enqueued, dispatched, cached, searched, embedded, or deleted across tenants.
- Exception: GDPR/right-to-erasure flows may intentionally delete all records for a data subject across tenants when that is the documented API contract. Make that all-tenant behavior explicit in the API docs, service/repository names or comments, logs, and tests; do not reuse it for normal tenant-owned workflows.
- When making a model, migration, API request, or repository change involving tenant-owned data, audit every downstream path that carries or derives from that data: handlers, services, repositories, message publishers, River job args, workers, webhook payloads, search, embeddings, bulk operations, logs, and metrics.
- Tenant access rules must be consistent across every path that can observe, mutate, derive from, or act on the same resource. If one API endpoint, repository method, search path, webhook dispatch path, worker, backfill, bulk operation, or export path requires tenant scope, every alternate path for that resource must enforce the same tenant boundary.
- Do not model `tenant_id` as an optional filter for tenant-owned resources. Prefer required tenant parameters in service/repository method signatures (`tenantID string`, not `*string`) unless the domain explicitly supports global resources and documents that behavior.
- Prefer tenant-aware repository/service methods for tenant-owned workflows. Avoid adding broad helpers that return all enabled/all matching resources when the caller is dispatching, processing, deriving, exporting, or exposing tenant data.
- Async jobs must carry the tenant boundary when the source data has one, and workers must re-check tenant scope before doing side effects. Do not rely only on enqueue-time filtering.
- Global resources may intentionally have `tenant_id = NULL` only when the domain explicitly documents them as non-tenant-owned. Webhooks are tenant-owned and must require a non-empty `tenant_id`; a missing tenant on event data must not match any webhook.
- When changing access rules for a tenant-owned model, search for all alternate access paths by resource name and by derived side effects: list, get, update, delete, bulk delete, webhook fan-out, River jobs, workers, embeddings, search, exports, cache invalidation, logs, and metrics.
- Every tenant-owned mutation must run through the tenant write lock helper (`internal/repository/tenant_write_lock.go`): a transaction that first try-acquires the shared advisory lock on `tenant_write|<len>:<tenant_id>` (resolve the row's tenant inside the transaction for ID-based mutations, then mutate with a tenant-scoped WHERE). The tenant data purge holds the same key exclusively. Lock order is fixed: tenant shared lock → scope-specific advisory locks (e.g. taxonomy run scope) → row locks; never block on an advisory lock while holding row locks. A path that skips the helper silently reopens the purge/write race.
- For any tenant-scoping change, include verification at the boundary where the leak could happen: database query behavior, service fan-out, worker execution, and API behavior when relevant. Include at least one alternate-path regression test proving that data allowed through the primary path cannot leak through async dispatch, bulk operations, derived indexes, exports, or background workers. Tests are the evidence; the invariant belongs in the architecture.

## Testing Guidelines
- Unit tests live beside the code in `internal/**` and need no database: `make test-unit`.
- Integration tests live under `tests/` and require a pgvector-enabled Postgres (`DATABASE_URL` pointing at a `test_db` with migrations applied): `make tests`. `make test-all` runs both.
- Name test files `*_test.go` and test functions `TestXxx`.
- Use `make tests-coverage` / `make check-coverage` when adding meaningful logic (the threshold excludes the `cmd/*` main packages).
- Tenant-isolation changes must include an alternate-path regression test (see Multi-Tenancy & Data Isolation).

## Commit & Pull Request Guidelines
- Always create a pull request for changes and don't commit directly to main.
- Commit messages follow a Conventional Commits style (e.g., `chore: fix linting errors`).
- PRs should describe the change, include test evidence (command + result), and link related issues when available.
- For API behavior changes, include request/response examples or screenshots of Swagger UI.

## Security & Configuration Tips
- Configure `API_KEY` and `DATABASE_URL` via `.env` or environment variables.
- Enrichment providers and other runtime options are env-configured (`EMBEDDING_*`, `TRANSLATION_*`, `SENTIMENT_*`, `EMOTIONS_*`, `TENANT_SETTINGS_CACHE_*`, `OTEL_*`); `.env.example` is the full, documented set.
- Do not commit `.env` or secrets; use `.env.example` as the base.
