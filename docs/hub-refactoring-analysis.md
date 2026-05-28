# Hub Refactoring Analysis

Date: 2026-05-20

This document summarizes the current shape of the Hub codebase and the refactoring opportunities found during a read-only architecture pass. The focus is on reducing duplication, making composition more systematic, tightening tenant boundaries, and using modern Go/tooling where it fits the project.

## Executive Summary

The Hub already has a clean layered shape:

- `cmd/api` wires HTTP, repositories, services, handlers, River workers, OpenAPI, and graceful shutdown.
- `cmd/worker` wires the background worker process.
- `internal/repository` owns SQL persistence.
- `internal/service` owns domain workflow.
- `internal/api/handlers` owns HTTP request/response behavior.
- `internal/workers` owns River worker registration and job execution.

The main issue is not architectural chaos. The issue is that useful patterns are emerging manually and repeatedly:

- repo/service/handler triplets are hand-wired in `cmd/api/app.go`;
- route registration is repeated between production and tests;
- repositories repeat pagination, dynamic filtering, placeholder management, update builders, and row scanning loops;
- handlers repeat UUID parsing, JSON decoding, query param parsing, and error mapping;
- app startup repeats embedding, webhook, River, observability, and shutdown wiring across `cmd/api`, `cmd/worker`, and `cmd/backfill-embeddings`.

The best next step is a small, explicit composition layer. Avoid a heavy automatic framework or reflection-based dependency injection. The codebase would benefit more from typed constructors, typed dependency structs, and shared registration helpers than from "magic" automation.

## Highest Priority Finding: Tenant Boundary Gaps

The repository guidelines state that `tenant_id` is a security boundary. Current code has several ID-based tenant-owned paths that do not appear tenant-scoped:

- feedback record get/update/delete paths use resource `id` without requiring `tenant_id`;
- webhook get/update/delete paths use resource `id` without requiring `tenant_id`;
- the OpenAPI paths for `/v1/feedback-records/{id}` and `/v1/webhooks/{id}` do not require `tenant_id`;
- repository/service methods mirror that broad access pattern.

This is the most important finding because it is behavioral, not cosmetic. UUID secrecy is not a tenant boundary.

Recommended direction:

- Add tenant-aware methods such as `GetByIDAndTenant`, `UpdateByIDAndTenant`, and `DeleteByIDAndTenant` for tenant-owned resources.
- Require tenant scope at the handler/service boundary for normal tenant-owned workflows.
- Keep any intentional all-tenant behavior explicit, named, documented, and tested. The GDPR/right-to-erasure path is the obvious exception.
- Add regression tests that prove tenant A cannot read, update, delete, dispatch, search, or derive tenant B data through alternate paths.

## Composition And Initialization

Current state:

- `cmd/api/app.go` is a large composition root. `NewApp` creates config-dependent providers, repositories, services, handlers, River workers, OpenAPI handlers, routes, server, and shutdown hooks.
- `cmd/worker/app.go` has parallel setup for worker-only dependencies.
- `cmd/backfill-embeddings/main.go` repeats embedding and worker setup concerns.
- `tests/integration_test.go` repeats production-like repository/service/handler/route setup because `cmd/api` is package `main` and not importable as a shared test helper.

Recommended direction:

- Introduce an internal composition package, for example `internal/app` or `internal/bootstrap`.
- Add explicit structs:
  - `Repositories`
  - `Services`
  - `Handlers`
  - `Workers`
  - `Providers`
- Use typed constructors such as:

```go
type Repositories struct {
	FeedbackRecords *repository.FeedbackRecordsRepository
	Webhooks        *repository.WebhooksRepository
	TenantData      *repository.TenantDataRepository
	Embeddings      *repository.EmbeddingsRepository
}

type Services struct {
	FeedbackRecords *service.FeedbackRecordsService
	Webhooks        *service.WebhooksService
	TenantData      *service.TenantDataService
	Search          *service.SearchService
}
```

- Keep constructors explicit. Do not introduce generic "model registration" or reflection-based dependency injection.
- Extract route registration into reusable functions so production and tests use the same registration path.

Good target:

```go
repos := bootstrap.NewRepositories(db)
services := bootstrap.NewServices(bootstrap.ServiceDeps{
	Repositories: repos,
	MessageQueue: queue,
	Embeddings:   embeddingDeps,
	Config:       cfg,
})
handlers := bootstrap.NewHandlers(services)
router := bootstrap.NewRouter(bootstrap.RouterDeps{
	Handlers: handlers,
	Config:   cfg,
})
```

This keeps initialization compact while preserving compile-time visibility.

## Endpoint Registration

Current state:

- Route registration is centralized enough to understand, but still manually coupled to app setup.
- Tests repeat pieces of handler and route wiring.
- Middleware and OpenAPI setup live close to production routing, which is good, but makes reuse harder.

Recommended direction:

- Extract route registration into typed functions:
  - `RegisterPublicRoutes(router, handlers, deps)`
  - `RegisterProtectedV1Routes(router, handlers, deps)`
  - `RegisterHealthRoutes(router, deps)`
- Alternatively use a typed route table for simple HTTP routes, while keeping custom middleware groups explicit.
- Ensure integration tests instantiate the same route registration functions as production.

Avoid:

- automatic route discovery;
- reflection over handler methods;
- deriving HTTP paths from model names.

Routes are security-sensitive and should remain explicit.

## Repository Duplication

There is meaningful duplication in repository code. The duplication is mostly SQL mechanics, not business concepts.

Repeated patterns:

- keyset pagination:
  - apply filters;
  - apply cursor condition;
  - order rows;
  - fetch `limit + 1`;
  - trim extra row;
  - calculate next cursor.
- placeholder-aware `WHERE` clause construction.
- dynamic update query construction.
- repeated `QueryContext`, `defer rows.Close`, scan loop, and `rows.Err` checks.
- `sql.ErrNoRows` mapping.
- repeated timestamp and nullable-field scanning.
- duplicated nearest-neighbor query variants in the embeddings repository.

Recommended direction:

- Add small internal SQL helpers rather than a generic repository base class.
- Consider a repository-local helper package, for example `internal/repository/sqlutil`.
- Useful helpers:
  - `WhereBuilder` for placeholder-safe filters;
  - `UpdateBuilder` for dynamic `SET` clauses;
  - `CollectRows[T]` for standard query/scan/rows error flow;
  - `KeysetPage[T]` or a small pagination helper that centralizes `limit + 1` behavior;
  - shared `NotFound` mapping helpers if error semantics are consistent.

Keep row scanners explicit and model-specific. That gives strong readability around SQL shape while removing the tedious mechanics.

Do not introduce a generic repository pattern such as `Repository[T]` for every model. The current SQL is domain-specific enough that this would hide important details.

## Service Layer Duplication

Current state:

- Services are mostly clean and consumer-oriented.
- `FeedbackRecordsService` already defines interfaces for dependencies it consumes, which is a good Go pattern.
- Some constructors are growing long positional argument lists.
- Search service already uses a params/deps struct style, which is a better pattern for complex construction.

Recommended direction:

- Standardize complex constructors on dependency structs:

```go
type FeedbackRecordsServiceDeps struct {
	RecordsRepo       FeedbackRecordsRepository
	EmbeddingsRepo    EmbeddingsRepository
	EmbeddingModel    string
	Publisher         EventPublisher
	EmbeddingInserter EmbeddingInserter
	Queue             Queue
	MaxAttempts       int
}
```

- Keep interfaces close to the consumer package, especially in `service`.
- Avoid forcing every model into a mandatory service interface if there is only one concrete implementation and no test seam is needed.
- Make tenant scope explicit in service method signatures for tenant-owned workflows.

## Handler Duplication And API Consistency

Current state:

- Handlers repeat UUID parsing, JSON decoding, query parsing, validation, and error response mapping.
- JSON decode error behavior differs between resources.
- Search query parsing silently clamps/defaults some invalid values, while list endpoints return `400` for invalid query params.
- Error response style is close, but not fully consistent.

Recommended direction:

- Add small HTTP helpers:
  - `DecodeJSONStrict`;
  - `ParseUUIDPath`;
  - `ParseRequiredQuery`;
  - `ParseLimit`;
  - `WriteServiceError`;
  - consistent problem detail helpers.
- Decide whether invalid query values should clamp/default or return `400`, then apply the same rule across endpoints.
- Keep request-specific validation close to the handler/model.
- Add endpoint tests for consistent error bodies.

## Webhook Model Inconsistency

Current state:

- Migrations require webhook `tenant_id` to be non-empty.
- The Go model still represents webhook tenant ID as optional (`*string`) in places.
- The repository and API allow broad ID-based operations.

Recommended direction:

- Make webhook domain/public model `TenantID` a required `string`.
- Keep pointers only in create/update request DTOs where optionality is semantically required.
- Consider whether webhook `tenant_id` should be mutable. If not, remove tenant mutation from update paths.
- Ensure missing tenant event data never matches any webhook.

## Async And Worker Boundaries

Current state:

- River worker registration already has a useful centralized dependency struct in `internal/workers/wiring.go`.
- Webhook delivery and embedding paths cross process boundaries and therefore need strong tenant propagation.

Recommended direction:

- Ensure every async job derived from tenant-owned data carries `tenant_id`.
- Workers should re-check tenant scope before side effects.
- Avoid broad repository helpers such as "all enabled webhooks" unless the domain explicitly supports global resources.
- Add tests at the worker or service fan-out boundary, not only at handler level.

## Tooling And Modern Go Opportunities

The project already has a strong tooling baseline:

- `golangci-lint` with strict linters;
- `gofumpt` and `gci`;
- `govulncheck`;
- Spectral for OpenAPI;
- Schemathesis for API contract testing;
- goose migrations;
- CI matrix for PostgreSQL versions;
- non-root Docker runtime;
- `slog` adoption.

Opportunities:

- Use Go tool directives to track executable tool dependencies in `go.mod` instead of manually duplicating versions across Makefile and CI.
- Pin local Schemathesis to the same version as CI.
- Pin `riverui` in Compose instead of using `latest`.
- Add `go test -race` for unit or selected packages in CI.
- Add `-shuffle=on` for tests that can tolerate it.
- Add native fuzz targets for:
  - cursor encode/decode;
  - search cursor decode;
  - field type parsing;
  - event type parsing;
  - tenant extraction;
  - blacklist/URL normalization;
  - JSON problem response decoding.
- Consider `sqlc` for stable static SQL, but do not force dynamic filtering or pgvector paths into generated code if readability suffers.
- Consider `oapi-codegen` if the team wants generated request/response/server types from OpenAPI. This is a bigger workflow decision, not a small refactor.

## Test Coverage And Test Structure

Observed local verification:

- `go test ./cmd/api ./internal/... ./pkg/...` passed.
- `go test ./cmd/api ./cmd/worker ./cmd/backfill-embeddings ./internal/... ./pkg/... -run '^$'` passed.
- `goose -dir migrations validate` passed.
- `govulncheck ./...` reported no vulnerabilities.
- Unit-only coverage for the sampled package set was low, around 32.8 percent. Full CI coverage may differ because it includes integration tests and different package selection.

Recommended direction:

- Add repository tests around tenant-scoped access methods.
- Add handler tests for consistent problem details and query parsing.
- Add unit tests for shared SQL helpers once introduced.
- Add integration tests that use the same route registration helper as production.
- Reduce integration test setup duplication with API client helpers.
- Keep regression tests close to the security boundary they protect.

## Suggested Refactor Plan

### Phase 1: Security First

- Add tenant-aware repository/service methods for feedback records and webhooks.
- Update handlers and OpenAPI to require tenant scope on tenant-owned ID operations.
- Add cross-tenant regression tests for get, update, delete, search, webhook dispatch, and embeddings where applicable.
- Make webhook domain tenant ID non-null.

### Phase 2: Composition Cleanup

- Create `internal/app` or `internal/bootstrap`.
- Extract `NewRepositories`, `NewServices`, `NewHandlers`, and route registration.
- Reuse route registration from integration tests.
- Keep `cmd/api` and `cmd/worker` as thin binaries.

### Phase 3: Repository SQL Helpers

- Add `WhereBuilder`, `UpdateBuilder`, row collection helpers, and pagination helpers.
- Apply them first to one repository to validate the shape.
- Roll them through feedback records, webhooks, and embeddings only after the first pass is clearly simpler.

### Phase 4: Handler Consistency

- Centralize strict JSON decode and common param parsing.
- Normalize API error response behavior.
- Add focused tests for invalid UUIDs, invalid JSON, invalid limits, and invalid cursor values.

### Phase 5: Tooling Hardening

- Move Go tool versions into `go.mod` tool directives.
- Pin local/Compose tool versions.
- Add fuzz targets for parser-like code.
- Add race/shuffle testing where practical.

## What Not To Do

- Do not add reflection-based dependency injection.
- Do not auto-register endpoints from model names.
- Do not introduce a generic repository base class for all models.
- Do not make tenant ID an optional filter for tenant-owned resources.
- Do not hide SQL shape behind abstractions that make security review harder.

The codebase is close to a good shape. The highest leverage path is to make the existing patterns explicit, typed, and shared, while tightening tenant boundaries before making broad mechanical cleanup.
