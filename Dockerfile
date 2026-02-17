# =============================================================================
# Stage 1: Build
# =============================================================================
# TARGETOS/TARGETARCH are set by Docker Buildx for multi-platform builds (e.g. linux/arm64 on Mac M1).
FROM golang:1.25.7-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH

RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Install goose and river CLI for migrations (for target platform)
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go install github.com/pressly/goose/v3/cmd/goose@v3.26.0 && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go install github.com/riverqueue/river/cmd/river@v0.30.2

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build the application
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /build/bin/api ./cmd/api

# =============================================================================
# Stage 2: Runtime
# =============================================================================
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -S app && adduser -S app -G app

WORKDIR /app

# Copy binary and migration tools from builder
COPY --from=builder /build/bin/api /app/api
COPY --from=builder /go/bin/goose /usr/local/bin/goose
COPY --from=builder /go/bin/river /usr/local/bin/river

# Copy migration files
COPY --from=builder /build/migrations /app/migrations

# Switch to non-root user
USER app

EXPOSE 8080

ENTRYPOINT ["/app/api"]
