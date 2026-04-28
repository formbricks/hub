.PHONY: all test help tests mcp-smoke tests-coverage check-coverage check-coverage-staged build build-api build-worker build-backfill-embeddings run run-worker run-backfill-embeddings init-db clean docker-up docker-down docker-clean deps install-tools fmt lint lint-new lint-openapi dev-setup test-all test-unit schemathesis install-hooks migrate-status migrate-validate river-migrate

# Aliases for checkmake/lint expectations
all: build
test: test-all

# Default target - show help
help:
	@echo "Available targets:"
	@echo "  make help             - Show this help message"
	@echo "  make dev-setup        - Set up development environment (docker, deps, tools, schema, hooks)"
	@echo "  make build                    - Build hub-api and hub-worker"
	@echo "  make build-api                - Build hub-api only (bin/hub-api)"
	@echo "  make build-worker             - Build hub-worker only (bin/hub-worker)"
	@echo "  make build-backfill-embeddings - Build the backfill-embeddings command"
	@echo "  make run              - Run the API server (hub-api)"
	@echo "  make run-worker       - Run the worker (hub-worker)"
	@echo "  make run-backfill-embeddings - Run the backfill-embeddings command (enqueues embedding jobs; loads .env)"
	@echo "  make test-unit        - Run unit tests (fast, no database)"
	@echo "  make tests            - Run integration tests"
	@echo "  make mcp-smoke        - Run the live MCP package smoke test (requires Hub env vars)"
	@echo "  make test-all         - Run all tests (unit + integration)"
	@echo "  make tests-coverage   - Run tests with coverage report"
	@echo "  make check-coverage   - Run tests and fail if coverage below COVERAGE_THRESHOLD (excludes cmd/api)"
	@echo "  make check-coverage-staged - Run coverage for staged production Go files"
	@echo "  make init-db          - Initialize database schema (run migrations with goose)"
	@echo "  make migrate-status   - Show migration status"
	@echo "  make migrate-validate - Validate migration files (no DB)"
	@echo "  make river-migrate    - Run River job queue migrations (required for webhook delivery)"
	@echo "  make fmt              - Format code (golangci-lint run --fix)"
	@echo "  make lint             - Run linter (includes format checks)"
	@echo "  make lint-new         - Run linter only on new code since base (default origin/main; for CI set LINT_BASE_REV to PR base SHA)"
	@echo "  make deps             - Install Go dependencies"
	@echo "  make install-tools    - Install development tools (golangci-lint, govulncheck, goose)"
	@echo "  make install-hooks    - Install git hooks"
	@echo "  make docker-up        - Start Docker containers"
	@echo "  make docker-down      - Stop Docker containers"
	@echo "  make docker-clean     - Stop Docker containers and remove volumes"
	@echo "  make clean            - Clean build artifacts"
	@echo "  make schemathesis     - Run Schemathesis API tests (requires API server running)"
	@echo "  make lint-openapi     - Lint OpenAPI spec with Spectral"

# Lint OpenAPI spec with Spectral
lint-openapi:
	npx --yes @stoplight/spectral-cli@6 lint openapi.yaml

# Run all tests (integration tests in tests/ directory).
# Loads .env if present so DATABASE_URL (e.g. port 5433) is used when Postgres runs on a non-default port.
tests:
	@echo "Running integration tests..."
	@(set -a && [ -f .env ] && . ./.env && set +a; go test ./tests/... -v -timeout 120s)

# Run the opt-in live MCP package smoke test.
# Requires HUB_API_KEY and FORMBRICKS_HUB_BASE_URL (or HUB_BASE_URL).
mcp-smoke:
	@echo "Running MCP package smoke test..."
	@(set -a && [ -f .env ] && . ./.env && set +a; RUN_MCP_SMOKE_TEST=1 go test ./tests -run TestMCPPackageSmoke -count=1 -v -timeout 120s)

# Run unit tests (fast, no database required)
test-unit:
	@echo "Running unit tests..."
	go test ./internal/... -v

# Run all tests (unit + integration)
test-all: test-unit tests
	@echo "All tests passed!"

# Run tests with coverage (unit + integration).
# cmd/api (app.go, main.go) is excluded—coverage is for internal/, pkg/, and tests/.
tests-coverage:
	@echo "Running tests with coverage..."
	go test ./internal/... ./pkg/... ./tests/... -v -cover -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Minimum coverage threshold (percent). Fails if coverage falls below this.
COVERAGE_THRESHOLD ?= 15
PRE_COMMIT_COVERAGE_THRESHOLD ?= 50

# Check coverage threshold (fail if below COVERAGE_THRESHOLD).
# Excludes cmd/api (app.go, main.go) from coverage. Includes internal/, tests/, and pkg/.
check-coverage:
	@echo "Running tests with coverage (threshold: $(COVERAGE_THRESHOLD)%)..."
	@(set -a && [ -f .env ] && . ./.env && set +a; go test ./internal/... ./pkg/... ./tests/... -coverprofile=coverage.out)
	@COV=$$(go tool cover -func=coverage.out | \tail -1 | awk '{gsub(/%/, ""); print $$3}') && \
	if [ -z "$$COV" ] || ! awk -v c="$$COV" -v t="$(COVERAGE_THRESHOLD)" 'BEGIN { exit (c+0 >= t) ? 0 : 1 }'; then \
		echo ""; \
		echo "❌ Coverage $$COV% is below threshold $(COVERAGE_THRESHOLD)%"; \
		go tool cover -func=coverage.out | tail -5; \
		exit 1; \
	fi && \
	echo "✅ Coverage $$COV% meets threshold $(COVERAGE_THRESHOLD)%"

# Check coverage for staged production Go files only. This keeps pre-commit focused on
# files touched by the commit instead of unrelated low-coverage code in the same package.
check-coverage-staged:
	@if [ -z "$(strip $(STAGED_PACKAGES))" ]; then \
		echo "No staged packages supplied; skipping staged coverage."; \
		exit 0; \
	fi; \
	STAGED_GO_FILES=$$(git diff --cached --name-only --diff-filter=ACM | grep '\.go$$' | grep -v '_test\.go$$' | grep -E '^(internal|pkg|tests)/' || true); \
	if [ -z "$$STAGED_GO_FILES" ]; then \
		echo "No staged production Go files; skipping staged coverage."; \
		exit 0; \
	fi; \
	STAGED_GO_FILE_LIST=$$(printf "%s\n" "$$STAGED_GO_FILES" | tr '\n' ' '); \
	echo "Running staged coverage (threshold: $(PRE_COMMIT_COVERAGE_THRESHOLD)%)..."; \
	go test $(STAGED_PACKAGES) -coverprofile=coverage.staged.out; \
	MODULE=$$(go list -m); \
	COV=$$(awk -v module="$$MODULE" -v files="$$STAGED_GO_FILE_LIST" '\
		BEGIN { split(files, fileList, " "); for (i in fileList) wanted[module "/" fileList[i]] = 1 } \
		NR == 1 { next } \
		{ split($$1, loc, ":"); if (wanted[loc[1]]) { stmts += $$2; if ($$3 > 0) covered += $$2 } } \
		END { if (stmts > 0) printf "%.1f", (covered / stmts) * 100 }' coverage.staged.out); \
	if [ -z "$$COV" ]; then \
		echo "❌ Could not calculate staged coverage for:"; \
		echo "$$STAGED_GO_FILES" | sed 's/^/    /'; \
		exit 1; \
	fi; \
	if ! awk -v c="$$COV" -v t="$(PRE_COMMIT_COVERAGE_THRESHOLD)" 'BEGIN { exit (c+0 >= t) ? 0 : 1 }'; then \
		echo ""; \
		echo "❌ Staged coverage $$COV% is below threshold $(PRE_COMMIT_COVERAGE_THRESHOLD)%"; \
		echo "Staged production Go files:"; \
		echo "$$STAGED_GO_FILES" | sed 's/^/    /'; \
		exit 1; \
	fi; \
	echo "✅ Staged coverage $$COV% meets threshold $(PRE_COMMIT_COVERAGE_THRESHOLD)%"

# Build hub-api and hub-worker
build: build-api build-worker
	@echo "Binaries created: bin/hub-api, bin/hub-worker"

# Build the API server (hub-api)
build-api:
	@echo "Building hub-api..."
	go build -o bin/hub-api ./cmd/api
	@echo "Binary created: bin/hub-api"

# Build the worker (hub-worker)
build-worker:
	@echo "Building hub-worker..."
	go build -o bin/hub-worker ./cmd/worker
	@echo "Binary created: bin/hub-worker"

# Build the backfill-embeddings command (enqueues embedding jobs; requires DATABASE_URL)
build-backfill-embeddings:
	@echo "Building backfill-embeddings..."
	go build -o bin/backfill-embeddings ./cmd/backfill-embeddings
	@echo "Binary created: bin/backfill-embeddings"

# Run the backfill-embeddings command (loads .env for DATABASE_URL etc.). Requires .env; fails fast if missing.
run-backfill-embeddings:
	@if [ ! -f .env ]; then echo "Error: .env file required. Copy .env.example to .env and configure."; exit 1; fi && \
	(set -a && . ./.env && set +a && go run ./cmd/backfill-embeddings)

# Run the API server (hub-api).
# Config: .env if present, else environment variables; env vars override .env. Copy .env.example to .env or set env vars.
run:
	@echo "Starting hub-api..."
	go run ./cmd/api

# Run the worker (hub-worker). Config: .env if present, else env vars (same as run). Requires DATABASE_URL; API_KEY not required.
run-worker:
	@echo "Starting hub-worker..."
	go run ./cmd/worker

# Initialize database schema (run goose migrations up)
init-db:
	@echo "Running migrations..."
	@command -v goose >/dev/null 2>&1 || { echo "Error: goose not found. Install with: make install-tools"; exit 1; }
	@if [ -f .env ]; then \
		export $$(grep -v '^#' .env | xargs) && \
		if [ -z "$$DATABASE_URL" ]; then \
			echo "Error: DATABASE_URL not found in .env file"; \
			exit 1; \
		fi && \
		goose -dir migrations postgres "$$DATABASE_URL" up; \
	else \
		if [ -z "$$DATABASE_URL" ]; then \
			echo "Error: DATABASE_URL environment variable is not set"; \
			echo "Please set it or create a .env file with DATABASE_URL"; \
			exit 1; \
		fi && \
		goose -dir migrations postgres "$$DATABASE_URL" up; \
	fi
	@echo "Migrations applied successfully"

# Show migration status (requires DATABASE_URL)
migrate-status:
	@command -v goose >/dev/null 2>&1 || { echo "Error: goose not found. Install with: make install-tools"; exit 1; }
	@if [ -f .env ]; then export $$(grep -v '^#' .env | xargs); fi && \
	if [ -z "$$DATABASE_URL" ]; then \
		echo "Error: DATABASE_URL not set"; exit 1; \
	fi && \
	goose -dir migrations postgres "$$DATABASE_URL" status

# Validate migration files (no database required)
migrate-validate:
	@command -v goose >/dev/null 2>&1 || { echo "Error: goose not found. Install with: make install-tools"; exit 1; }
	@goose -dir migrations validate
	@echo "Migration files are valid"

# Run River job queue migrations (required for webhook delivery)
river-migrate:
	@command -v river >/dev/null 2>&1 || { echo "Error: river CLI not found. Install with: make install-tools or go install github.com/riverqueue/river/cmd/river@$(RIVER_VERSION)"; exit 1; }
	@if [ -f .env ]; then \
		export $$(grep -v '^#' .env | xargs) && \
		if [ -z "$$DATABASE_URL" ]; then echo "Error: DATABASE_URL not found in .env"; exit 1; fi && \
		river migrate-up --database-url "$$DATABASE_URL"; \
	else \
		if [ -z "$$DATABASE_URL" ]; then echo "Error: DATABASE_URL not set"; exit 1; fi && \
		river migrate-up --database-url "$$DATABASE_URL"; \
	fi
	@echo "River migrations applied"

# Start Docker containers
docker-up:
	@echo "Starting Docker containers..."
	docker compose up -d
	@echo "Waiting for services to be ready..."
	@sleep 3
	@docker compose ps

# Stop Docker containers
docker-down:
	@echo "Stopping Docker containers..."
	docker compose down

# Stop and remove volumes
docker-clean:
	@echo "Stopping Docker containers and removing volumes..."
	docker compose down -v

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy
	@echo "Dependencies installed"

# Install development tools
# Tool versions - update these periodically
GOLANGCI_LINT_VERSION := v2.11.4
GOVULNCHECK_VERSION := v1.3.0
GOOSE_VERSION := v3.27.1
RIVER_VERSION := v0.35.0
# Use pinned path so lint uses the version from make install-tools, not PATH
GOLANGCI_LINT ?= $(HOME)/go/bin/golangci-lint

install-tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
	go install github.com/riverqueue/river/cmd/river@$(RIVER_VERSION)
	@echo "Tools installed (golangci-lint $(GOLANGCI_LINT_VERSION), govulncheck $(GOVULNCHECK_VERSION), goose $(GOOSE_VERSION), river $(RIVER_VERSION))"

# Format code (golangci-lint applies gofumpt + gci from .golangci.yml formatters)
fmt:
	@echo "Formatting code..."
	@test -x $(GOLANGCI_LINT) || { echo "Error: golangci-lint not found. Install with: make install-tools"; exit 1; }
	$(GOLANGCI_LINT) run --fix ./...
	@echo "Code formatted"

# Lint code
lint:
	@echo "Linting code..."
	@test -x $(GOLANGCI_LINT) || { echo "Error: golangci-lint not found. Install with: make install-tools"; exit 1; }
	$(GOLANGCI_LINT) run ./...

# Lint only new code since base branch (for CI: fail on new issues, not existing ones).
# Default: origin/main so results don't depend on stale local main. When LINT_BASE_REV
# is origin/main we fetch first so the baseline is up to date; in CI set LINT_BASE_REV
# to the PR base commit SHA (e.g. GITHUB_BASE_SHA) for determinism.
LINT_BASE_REV ?= origin/main
lint-new:
	@echo "Linting new code since $(LINT_BASE_REV)..."
	@test -x $(GOLANGCI_LINT) || { echo "Error: golangci-lint not found. Install with: make install-tools"; exit 1; }
	@if [ "$(LINT_BASE_REV)" = "origin/main" ]; then git fetch origin main; fi
	$(GOLANGCI_LINT) run --new-from-rev=$(LINT_BASE_REV) ./...

# Install git hooks from .githooks directory
install-hooks:
	@echo "Installing git hooks..."
	@if [ -d .githooks ]; then \
		cp .githooks/pre-commit .git/hooks/pre-commit && \
		chmod +x .git/hooks/pre-commit && \
		echo "✅ Git hooks installed successfully"; \
	else \
		echo "Error: .githooks directory not found"; \
		exit 1; \
	fi

# Run everything needed for development
dev-setup: docker-up deps install-tools init-db install-hooks
	@echo "Development environment ready!"
	@echo "Set API_KEY environment variable for authentication"
	@echo "Run 'make run' to start the API server"

# Run Schemathesis API tests (all phases for thorough local testing)
# Phases: examples (schema examples), coverage (boundary values), stateful (API sequences), fuzzing (random)
# This runs more thorough tests than CI to find edge-case bugs.
# Requires: API server running (make run in another terminal)
# Requires: uvx (install via: curl -LsSf https://astral.sh/uv/install.sh | sh)
# Uses PORT from .env if set, otherwise 8080.
schemathesis:
	@echo "Running Schemathesis API tests (all phases)..."
	@echo "This is deeper testing than CI - may find edge-case bugs."
	@export PATH="$$HOME/.local/bin:$$PATH" && \
	if [ -f .env ]; then \
		export $$(grep -v '^#' .env | xargs) && \
		if [ -z "$$API_KEY" ]; then \
			echo "Warning: API_KEY not found in .env file, tests may fail authentication"; \
		fi && \
		uvx schemathesis run ./openapi.yaml \
			--url "http://localhost:$${PORT:-8080}" \
			--header "Authorization: Bearer $${API_KEY:-test-api-key-12345}" \
			--checks all \
			--phases examples,coverage,stateful,fuzzing \
			--max-examples 50; \
	else \
		if [ -z "$$API_KEY" ]; then \
			echo "Error: API_KEY environment variable is not set and .env file not found"; \
			echo "Please set API_KEY or create a .env file"; \
			exit 1; \
		fi && \
		uvx schemathesis run ./openapi.yaml \
			--url "http://localhost:$${PORT:-8080}" \
			--header "Authorization: Bearer $$API_KEY" \
			--checks all \
			--phases examples,coverage,stateful,fuzzing \
			--max-examples 50; \
	fi
