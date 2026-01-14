# Ensure Go binaries are in PATH
export PATH := $(PATH):/usr/local/go/bin:$(shell go env GOPATH 2>/dev/null || echo ~/go)/bin

.PHONY: help dev build lint ent-gen test clean docker-up docker-down install-tools setup generate-openapi

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
	@echo ''
	@echo 'Quick Start:'
	@echo '  1. make setup     # First time only'
	@echo '  2. make dev       # Start the service'

setup: ## First-time setup: create .env, install deps, start DB
	@echo "🔧 Setting up Hub service..."
	@if [ ! -f .env ]; then \
		cp env.example .env; \
		echo "✅ Created .env file"; \
	else \
		echo "✅ .env file already exists"; \
	fi
	@echo "📦 Installing Go dependencies..."
	@go mod download
	@echo "🛠️  Installing development tools..."
	@go install github.com/air-verse/air@latest
	@go install entgo.io/ent/cmd/ent@latest
	@echo "🐘 Starting PostgreSQL..."
	@docker-compose up -d postgres
	@sleep 5
	@echo "🔧 Enabling pgvector extension..."
	@docker exec formbricks_postgres psql -U formbricks -d hub_dev -c "CREATE EXTENSION IF NOT EXISTS vector;" || echo "⚠️  Warning: Could not create extension. Database may already exist."
	@echo ""
	@echo "✅ Setup complete!"
	@echo ""
	@echo "Next: Run 'make dev' to start the service"

dev: ## Start the development server (loads .env automatically)
	@if [ ! -f .env ]; then \
		echo "❌ Error: .env file not found"; \
		echo "Run 'make setup' first"; \
		exit 1; \
	fi
	@echo "🚀 Starting Hub service..."
	@set -a; source .env; set +a; go run ./cmd/hub

build: ## Build the binary
	go build -o bin/hub ./cmd/hub

# Increase timeout so CI golangci-lint step doesn't flake on slow module loading
lint: ## Run Go linters (with generous timeout for module loading)
	golangci-lint run --timeout 5m ./...

generate-openapi: ## Generate OpenAPI spec and save to root and docs
	@echo "📋 Generating OpenAPI specification..."
	@set -a; source .env 2>/dev/null || true; set +a; go run ./cmd/generate-openapi --yaml > openapi.yaml
	@mkdir -p docs/static/openapi
	@cp openapi.yaml docs/static/openapi/hub.yaml
	@echo "✅ OpenAPI spec saved to openapi.yaml and docs/static/openapi/hub.yaml"

ent-gen: ## Generate Ent code from schema
	@which ent > /dev/null || (echo "Error: Ent CLI not installed. Run 'make install-deps' first" && exit 1)
	go generate ./internal/ent

migrate: ## Run database migrations
	go run ./cmd/hub migrate

test: ## Run all tests
	go test -v -race -cover ./...

test-coverage: ## Run tests with coverage report
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report generated: coverage.html"

clean: ## Clean build artifacts and test coverage
	rm -rf bin/ tmp/ coverage.out coverage.html

docker-up: ## Start PostgreSQL with Docker Compose
	docker-compose up -d postgres

docker-down: ## Stop Docker Compose services (PostgreSQL only)
	docker-compose down postgres

enable-pgvector: ## Enable pgvector extension in database (run after docker-up)
	@echo "🔧 Enabling pgvector extension..."
	@docker exec formbricks_postgres psql -U formbricks -d hub_dev -c "CREATE EXTENSION IF NOT EXISTS vector;" || echo "❌ Error: Could not create extension"

install-deps: ## Install Go dependencies and dev tools
	go mod download
	go install github.com/air-verse/air@latest
	go install entgo.io/ent/cmd/ent@latest
	@echo ""
	@echo "✅ Dependencies installed!"
	@echo "Note: Make sure $(shell go env GOPATH)/bin is in your PATH"
	@echo "Add to ~/.zshrc: export PATH=\$$PATH:/usr/local/go/bin:\$$(go env GOPATH)/bin"
