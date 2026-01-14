# Repository Guidelines

## Project Structure & Module Organization
This is a Go repository with the API entrypoint in `cmd/hub` and domain code under `internal/`. Documentation lives in the `docs/` directory as a Docusaurus site that also hosts OpenAPI outputs in `static/openapi/`. The primary OpenAPI spec is also available at the root as `openapi.yaml`. Docker and CI helper files are at the root (`docker-compose.yml`, `Dockerfile`).

## Build, Test, and Development Commands
Use `make setup` to copy `.env`, install Go tools, and start PostgreSQL via Docker. `make dev` runs the API with hot reload (via Air). `make build` compiles the binary to `bin/hub`. `make generate-openapi` refreshes the OpenAPI spec for the docs site. For documentation, run `pnpm install` and `pnpm dev` inside the `docs/` directory.

## Coding Style & Naming Conventions
Go code must stay `gofmt`-clean with package names in lowercase. Generated Ent code belongs under `internal/ent`. Keep environment example templates in `env.example` and store secrets only in local `.env`. For docs TypeScript/React code, use 2-space indentation and PascalCase for React components while keeping file names kebab-case.

## Testing Guidelines
Primary tests are Go unit/integration suites located beside implementations as `*_test.go`. Run `make test` for verbose race-and-cover runs. Generate HTML coverage when needed with `make test-coverage` (outputs `coverage.html`). When touching docs components, run lint and type checks inside the `docs/` directory.

## Commit & Pull Request Guidelines
The project uses Conventional Commits (e.g., `feat(api): add survey endpoints`). Keep subjects under 72 characters and scope by domain (`api`, `docs`, `docker`, `config`). Before opening a PR, ensure lint and tests pass; include a concise summary, linked issue references, and screenshots for UI changes. Note any configuration impacts (e.g., new env vars) and mention follow-up migration steps when required.
