# =============================================================================
# Stage 1: Build
# =============================================================================
# TARGETOS/TARGETARCH are set by Docker Buildx for multi-platform builds (e.g. linux/arm64 on Mac M1).
FROM golang:1.26.4-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG GOOSE_VERSION=v3.27.1
ARG RIVER_VERSION=v0.39.0
ARG X_CRYPTO_VERSION=v0.52.0
ARG X_NET_VERSION=v0.55.0
ARG X_SYS_VERSION=v0.45.0

RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Install goose and river CLI for migrations (for target platform).
# Build them through a temporary module so we can force patched transitive
# versions even when the CLI upstream has not released a dependency bump.
RUN mkdir -p /tmp/migration-tools && \
    cd /tmp/migration-tools && \
    go mod init migration-tools && \
    go get \
      github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION} \
      github.com/riverqueue/river/cmd/river@${RIVER_VERSION} \
      golang.org/x/crypto@${X_CRYPTO_VERSION} \
      golang.org/x/net@${X_NET_VERSION} \
      golang.org/x/sys@${X_SYS_VERSION} && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go install github.com/pressly/goose/v3/cmd/goose && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go install github.com/riverqueue/river/cmd/river

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build the application (hub-api and hub-worker)
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /build/bin/hub-api ./cmd/api && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /build/bin/hub-worker ./cmd/worker

# =============================================================================
# Stage 2: Runtime (default: hub-api)
# =============================================================================
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata wget

# Create non-root user
RUN addgroup -S app && adduser -S app -G app

WORKDIR /app

# Copy binaries and migration tools from builder
COPY --from=builder /build/bin/hub-api /app/hub-api
COPY --from=builder /build/bin/hub-worker /app/hub-worker
COPY --from=builder /go/bin/goose /usr/local/bin/goose
COPY --from=builder /go/bin/river /usr/local/bin/river

# Copy migration files
COPY --from=builder /build/migrations /app/migrations
COPY --from=builder /build/openapi.yaml /app/openapi.yaml

# Switch to non-root user
USER app

EXPOSE 8080

# Health check for hub-api. Disable or override when running hub-worker (e.g. docker run ... hub-worker)
# since workers do not expose HTTP.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Default: run hub-api. Override with command to run hub-worker: docker run ... hub-worker
ENTRYPOINT ["/app/hub-api"]
