---
name: Connector Extensibility Research
overview: Research and analyze different connector extensibility approaches for the Hub, evaluating Go plugins, WASM runtimes, external containers, and other options for Input, Enrichment, and Output connectors.
todos:
  - id: research-go-plugins
    content: Research Go native plugin package limitations and use cases
    status: completed
  - id: research-rpc-plugins
    content: Research HashiCorp go-plugin framework and gRPC plugin patterns
    status: completed
  - id: research-wasm
    content: Research WebAssembly runtime options (Extism, Wasmtime) for Go
    status: completed
  - id: research-containers
    content: Research container-based extensibility patterns (sidecar, microservices)
    status: completed
  - id: research-scripting
    content: Research embedded scripting languages (Lua, JavaScript) for Go
    status: completed
  - id: research-http-grpc
    content: Research HTTP/gRPC service-based connector patterns
    status: completed
  - id: research-polling-webhooks
    content: Research polling vs webhook architecture patterns for data ingestion
    status: completed
  - id: research-ai-generation
    content: Research AI code generation approaches for connector development automation
    status: completed
  - id: analyze-connector-types
    content: Analyze requirements for Input, Enrichment, and Output connector types
    status: completed
  - id: create-comparison
    content: Create comparison matrix and recommendations by connector type
    status: completed
  - id: document-findings
    content: Document research findings and recommendations in plan
    status: completed
isProject: false
---

# Connector Extensibility Research Plan

## Implementation Status Summary

| Component | Status | Location |
|-----------|--------|----------|
| PollingInputConnector interface | ✅ Done | `internal/connector/polling.go` |
| WebhookInputConnector interface | ✅ Done | `internal/connector/webhook.go` |
| Poller (polling scheduler) | ✅ Done | `internal/connector/polling.go` |
| WebhookRouter (webhook routing) | ✅ Done | `internal/connector/webhook.go` |
| Webhook HTTP handler | ✅ Done | `internal/api/handlers/webhook_handler.go` |
| Formbricks SDK | ✅ Done | `pkg/formbricks/` |
| Formbricks Polling Connector | ✅ Done | `internal/connector/formbricks/connector.go` |
| Formbricks Webhook Connector | ✅ Done | `internal/connector/formbricks/webhook_connector.go` |
| Data Transformer | ✅ Done | `internal/connector/formbricks/transformer.go` |
| Integration Tests | ✅ Done | `tests/webhook_integration_test.go` |
| EnrichmentConnector interface | ⏳ Pending | - |
| OutputConnector interface | ⏳ Pending | - |
| Event Bus | ⏳ Pending | - |
| Retry Mechanism | ⏳ Pending | - |

**Terminology**:
- `PollingInputConnector` - Interface for polling-based connectors (formerly PullInputConnector)
- `WebhookInputConnector` - Interface for webhook-based connectors (formerly PushInputConnector)

**Environment Variables**:
- `FORMBRICKS_POLLING_API_KEY` - API key for Formbricks polling connector
- `FORMBRICKS_WEBHOOK_API_KEY` - API key for authenticating webhook requests
- `FORMBRICKS_SURVEY_ID` - Survey ID to poll
- `FORMBRICKS_URL` - Formbricks API base URL (optional, has default)

---

## Current Architecture Context

The Hub is a Go-based API service with:

- Simple architecture using `net/http` and `pgx`
- Clean separation: handlers → service → repository
- PostgreSQL for data persistence
- Performance-first design (hundreds of millions of records/day)
- Webhook-based event communication (planned)
- Headless/API-only design

## Project Goals & Requirements

**Context**: 4-Day Hackathon to improve the Hub

**Primary Goal**: Enable rapid connector development (15-minute target) to offer custom integrations as a service to Enterprise customers.

**Team**: Tiago & Anshuman

**Key Requirements**:

1. Support multiple connector types (Input, Enrichment, Output)
2. Enable AI-assisted connector generation for fast development
3. Support both webhook and polling patterns for Input connectors
4. Build example connectors in [hub-playground repository](https://github.com/formbricks/hub-playground)
5. Maximize out-of-the-box integrations for adoption

**Hackathon Scope** (4 days):

- Focus on MVP implementation that demonstrates the concept
- **Simplified approach**: In-process Go interfaces (fastest to implement)
- Implement core patterns (polling + webhooks for Input connectors)
- Build 2-3 example connectors to prove the concept
- Document architecture and patterns for future development

**Hackathon Success Criteria**:

- ✅ Working connector system with registration and execution - ACHIEVED
- ⏳ At least 2 working connectors (Formbricks Input + OpenAI Enrichment) - Formbricks DONE, OpenAI pending
- ✅ Webhook endpoint routing functional - ACHIEVED (`POST /webhooks/{connector}?apiKey=<key>`)
- ✅ Polling scheduler functional - ACHIEVED (Poller with configurable interval)
- ⏳ End-to-end data flow: Input Connector → Hub (write record) → Event → Output Connector → External System - Input→Hub DONE, Event system pending
- ⏳ Event system: Feedback record writes trigger events that output connectors process - PENDING
- ⏳ Documentation for connector development - PENDING

**Connector Modes to Support** (per connector):

- ✅ **Polling**: REST endpoint polling at intervals - connector actively fetches data (IMPLEMENTED)
- ✅ **Webhook**: HTTP POST callback - external source pushes data to connector (IMPLEMENTED)
- ⏭️ WebSockets (future - can mention in presentation but not required for MVP)

**Note**: Input connectors are split into two separate types: `PollingInputConnector` (polling) and `WebhookInputConnector` (webhook). This provides type safety - connectors implement exactly one interface, not both.

## Connector Types to Support

1. **Input Connectors**: Ingest data from external services (Formbricks, Typeform, App Store, Play Store) via webhooks OR API polling
   - Example: Formbricks connector calls responses management API, gets responses, writes feedback records to Hub
   - Each connector is configured as EITHER polling (pull) OR webhook (push), not both
   - Different connector instances can use different modes (e.g., `formbricks-polling` vs `formbricks-webhook`)

2. **Enrichment Connectors**: Transform/enrich data using LLMs, ML models, external services, or custom logic
   - Example: OpenAI sentiment analysis enrichment connector
   - May include WASM-based custom code/binary execution

3. **Output Connectors**: Send enriched data to third-party systems (data lakes, CRMs, etc.)
   - Triggered by events when feedback records are written to Hub
   - Must handle data transformation to match destination requirements
   - Event-driven: Hub sends message/event when record is created/updated → Output connectors process event

## Connector Architecture Pattern

Based on pseudocode provided, connectors follow a callback pattern with type-specific interfaces:

```go
// Polling Input Connector interface (polling-based) - IMPLEMENTED
type PollingInputConnector interface {
    // Poll external source and write data to Hub
    Poll(ctx context.Context) error
}

// Webhook Input Connector interface (webhook-based) - IMPLEMENTED
type WebhookInputConnector interface {
    // Handle webhook payload from external source
    HandleWebhook(ctx context.Context, payload []byte) error
}

// Enrichment Connector interface
type EnrichmentConnector interface {
    // Enrich a feedback record
    Enrich(ctx context.Context, record *FeedbackRecord) (*FeedbackRecord, error)

    GetName() string
    GetConfig() map[string]interface{}
}

// Output Connector interface (event-driven)
type OutputConnector interface {
    // Handle event when feedback record is written to Hub
    HandleEvent(ctx context.Context, event FeedbackRecordEvent) error

    // Optional: Batch processing
    HandleBatch(ctx context.Context, events []FeedbackRecordEvent) error

    GetName() string
    GetConfig() map[string]interface{}
}

// Retry configuration for connectors
type RetryConfig struct {
    MaxRetries      int           // Maximum number of retry attempts
    InitialDelay   time.Duration // Initial delay before first retry
    MaxDelay        time.Duration // Maximum delay between retries
    BackoffFactor   float64       // Exponential backoff multiplier
    RetryableErrors []error       // List of errors that should trigger retry
}

// Event sent when feedback record is written
type FeedbackRecordEvent struct {
    EventType string          // "created" or "updated"
    Record    *FeedbackRecord // The feedback record that was written
    Timestamp time.Time      // When the event occurred
}

// Registration pattern - IMPLEMENTED via WebhookRouter
// WebhookRouter.Register(name, connector, apiKey) for webhook connectors
// Poller.Start(ctx, connector) for polling connectors

// Usage examples (actual implementation)
// Polling: Uses Poller with PollingInputConnector
poller := connector.NewPoller(pollInterval, "formbricks")
poller.Start(ctx, formbricksConnector)

// Webhook: Uses WebhookRouter with WebhookInputConnector
webhookRouter := connector.NewWebhookRouter()
webhookRouter.Register("formbricks", formbricksWebhookConnector, apiKey)

// Polling connector callback pattern (implemented)
func (c *Connector) Poll(ctx context.Context) error {
    responses, err := c.client.GetResponses(...)  // Call Formbricks API
    feedbackRecords := TransformResponseToFeedbackRecords(response)
    c.feedbackService.CreateFeedbackRecord(ctx, recordReq)  // Write to Hub
}

type IngestionPattern string

const (
    PatternPolling   IngestionPattern = "polling"
    PatternWebhook   IngestionPattern = "webhook"
    PatternQueue     IngestionPattern = "queue"      // Future
    PatternSSE       IngestionPattern = "sse"        // Future
    PatternWebSocket IngestionPattern = "websocket"    // Future
    PatternCDC       IngestionPattern = "cdc"         // Future
)

type EnrichmentConnector interface {
    // Enrich a feedback record
    Enrich(ctx context.Context, record *FeedbackRecord) (*FeedbackRecord, error)
    // Get connector metadata
    GetConfig() EnrichmentConnectorConfig
}

type OutputConnector interface {
    // Send data to external system
    Send(ctx context.Context, records []*FeedbackRecord) error
    // Get connector metadata
    GetConfig() OutputConnectorConfig
}

// Type-specific registration functions (already defined above)
// Usage examples shown in main interface section
```

**Execution Models** (IMPLEMENTED):

- **Polling**: Hub Poller calls `connector.Poll()` at intervals (IMPLEMENTED)

  ```go
  // Implemented in internal/connector/polling.go
  type Poller struct {
      interval time.Duration
      name     string
  }
  
  func (p *Poller) Start(ctx context.Context, connector PollingInputConnector) {
      // Polls immediately on startup, then continues at interval
      // Stops when context is cancelled
  }
  ```

  - Uses `connector.NewPoller(interval, name)` to create poller
  - Polls immediately on startup, then at configured interval
  - Graceful shutdown via context cancellation

- **Webhook**: Hub receives HTTP POST and routes to `connector.HandleWebhook()` (IMPLEMENTED)

  ```go
  // Implemented in internal/connector/webhook.go
  type WebhookRouter struct {
      connectors map[string]registeredConnector
  }
  
  // Endpoint: POST /webhooks/{connector}?apiKey=<key>
  func (r *WebhookRouter) Route(ctx, name, payload) error
  func (r *WebhookRouter) ValidateAPIKey(name, apiKey) bool
  ```

  - Connector implements `WebhookInputConnector` interface
  - External source pushes data to Hub via `/webhooks/{connector}?apiKey=<key>`
  - WebhookRouter validates API key and routes to appropriate connector

- **Enrichment**: Hub calls enrichment connector during record processing (Day 3)
- **Output (Event-Driven)**: When feedback record is written to Hub:
  1. Hub creates `FeedbackRecordEvent` (event type: "created" or "updated")
  2. Hub sends event to event bus/channel
  3. Registered output connectors receive event
  4. Each output connector processes event via `HandleEvent()`
  5. Output connector transforms and sends data to external system

**Event-Driven Flow**:

```
Input Connector → Hub API (POST /v1/feedback-records)
                      ↓
              Service.CreateFeedbackRecord()
                      ↓
              Repository.Create() → Database
                      ↓
              Emit FeedbackRecordEvent("created", record)
                      ↓
              Event Bus/Channel
                      ↓
        ┌─────────────┴─────────────┐
        ↓                           ↓
Output Connector 1          Output Connector 2
(Salesforce)                (Data Lake)
        ↓                           ↓
External System 1          External System 2
```

**Note**: Input connectors are split into two types: `PollingInputConnector` (polling) and `WebhookInputConnector` (webhook). This provides type safety and simpler interfaces.

## Research Areas

### 1. Go Native Plugin System (`plugin` package)

**Technical Details:**

- Built into Go standard library
- Loads `.so` shared libraries at runtime
- Direct function calls with shared memory

**Pros:**

- Native Go performance (no serialization overhead)
- Direct data structure sharing
- Simple integration with existing Go codebase

**Cons:**

- Platform limitations: Only Linux, FreeBSD, macOS (no Windows)
- Cannot unload plugins once loaded
- Requires exact Go version match between Hub and plugins
- Build tag and dependency version conflicts cause runtime crashes
- Poor race detector support
- Complex deployment (must rebuild plugins when Hub updates)

**Use Case Fit:**

- ❌ Poor fit: Version coupling issues problematic for production
- ⚠️ Limited: Platform restrictions exclude Windows deployments

### 2. RPC-Based Plugins (HashiCorp go-plugin)

**Technical Details:**

- Process-isolated plugins via gRPC or net/rpc
- Plugins run as separate processes
- JSON/Protocol Buffers serialization
- Used by Terraform, Vault, Nomad, Boundary

**Pros:**

- Cross-platform (Linux, macOS, Windows)
- Process isolation (plugin crashes don't affect Hub)
- Hot-reloadable (restart plugins without restarting Hub)
- Resource limits via cgroups/containers
- Language-agnostic plugins (Python, Node.js, Rust, etc.)
- Version compatibility handled through compatibility tables
- Production-proven at scale
- ~50-100 microseconds RPC overhead (acceptable for most use cases)

**Cons:**

- RPC latency overhead vs shared libraries
- More complex deployment (separate processes)
- Requires process management infrastructure

**Use Case Fit:**

- ✅ Excellent for Input connectors (webhook handlers, API pollers)
- ✅ Good for Enrichment connectors (LLM calls, external API calls)
- ✅ Good for Output connectors (HTTP clients to external systems)

### 3. WebAssembly (WASM) Runtime

**Technical Details:**

- Extism Go SDK (`github.com/extism/go-sdk`)
- Sandboxed execution environment
- WASM modules loaded from files/remote sources
- Host functions enable bidirectional communication

**Pros:**

- Strong security isolation (sandboxed execution)
- Cross-platform (runs anywhere Go runs)
- Language-agnostic (compile any language to WASM)
- Small plugin sizes
- Hot-reloadable
- Good for untrusted code execution
- Growing ecosystem (Extism, Wasmtime, etc.)

**Cons:**

- Performance overhead vs native code (WASM interpretation/JIT)
- Limited system access (by design, for security)
- May need host functions for complex operations (HTTP, database access)
- Less mature ecosystem than RPC plugins
- Learning curve for plugin developers

**Use Case Fit:**

- ✅ Excellent for Enrichment connectors (data transformation, simple logic)
- ⚠️ Limited for Input/Output connectors (may need host functions for HTTP/network)

### 4. External Containers (Microservices)

**Technical Details:**

- Plugins run as separate containerized services
- Communication via HTTP/gRPC
- Kubernetes-native (sidecar pattern)
- Full process isolation

**Pros:**

- Complete isolation (containers can't affect Hub)
- Language-agnostic (any language/framework)
- Independent scaling per connector
- Independent deployment and versioning
- Resource limits (CPU, memory) per connector
- Standard microservices patterns
- Easy to monitor and debug separately
- Can leverage existing container orchestration

**Cons:**

- Highest operational complexity (container management)
- Network latency overhead
- Requires container orchestration (Docker, Kubernetes)
- More infrastructure overhead (more containers to manage)
- Higher resource usage (each connector = separate container)

**Use Case Fit:**

- ✅ Excellent for all connector types
- ✅ Best for production at scale
- ⚠️ Overkill for simple transformations

### 5. Embedded Scripting Languages (Lua/JavaScript)

**Technical Details:**

- GopherLua (`github.com/yuin/gopher-lua`) for Lua
- Otto or similar for JavaScript
- Scripts embedded in Hub process
- Direct function calls

**Pros:**

- Lightweight (~200KB for Lua VM)
- Fast execution (interpreted but optimized)
- Easy to embed and use
- Lower barrier to entry for plugin developers
- Good for simple transformation logic
- No separate process management

**Cons:**

- Limited to simple logic (not suitable for complex integrations)
- Security concerns (scripts run in Hub process)
- Limited ecosystem compared to full languages
- Not suitable for long-running operations
- Debugging can be challenging

**Use Case Fit:**

- ✅ Good for simple Enrichment connectors (data transformations)
- ❌ Not suitable for Input/Output connectors (need network access)

### 6. HTTP/gRPC Service Pattern

**Technical Details:**

- Connectors as independent HTTP/gRPC services
- Hub calls connectors via HTTP/gRPC
- Standard REST or gRPC APIs
- Can be deployed anywhere

**Pros:**

- Standard, well-understood pattern
- Language-agnostic
- Easy to develop and test
- Can be deployed independently
- Good observability (standard HTTP metrics)
- Works with existing infrastructure

**Cons:**

- Network latency
- Requires service discovery/configuration
- More operational overhead
- No built-in plugin management

**Use Case Fit:**

- ✅ Good for all connector types
- ✅ Best for external/third-party connectors
- ⚠️ Requires service registry/configuration management

## Comparison Matrix

| Approach | Performance | Isolation | Cross-Platform | Hot Reload | Complexity | Best For |

|----------|------------|-----------|----------------|------------|------------|----------|

| Go `plugin` | ⭐⭐⭐⭐⭐ | ⭐⭐ | ⭐⭐ | ❌ | ⭐⭐⭐ | N/A (too limited) |

| RPC (go-plugin) | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ✅ | ⭐⭐⭐ | Input/Enrichment/Output |

| WASM | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ✅ | ⭐⭐⭐⭐ | Enrichment (simple) |

| Containers | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ✅ | ⭐⭐ | All (production scale) |

| Scripting | ⭐⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐⭐⭐ | ✅ | ⭐⭐⭐⭐⭐ | Enrichment (simple) |

| HTTP/gRPC | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ✅ | ⭐⭐⭐ | All (external) |

## Recommendations by Connector Type

### Input Connectors

**Hackathon Recommendation: HTTP Service Pattern (Fastest to Implement)**

- Connectors as separate HTTP services (can be same process or separate)
- Hub calls connectors via HTTP POST
- Simplest to implement in 4 days
- Easy to test and debug
- Can evolve to RPC plugins later

**Alternative for Hackathon: In-Process Go Interfaces (Fastest MVP)**

- Connectors as Go packages compiled into Hub
- Direct function calls (no network overhead)
- Fastest to prototype
- Good for proving concept
- Can refactor to plugins later

**Post-Hackathon: RPC-based (go-plugin) or External Containers**

- Need webhook handling and API polling
- May need long-running processes
- Process isolation important
- RPC plugins: Good balance of performance and isolation
- Containers: Best for production scale and independent scaling

### Enrichment Connectors

**Primary Recommendation: Hybrid Approach**

- **Simple transformations**: WASM or Lua scripting (fast, lightweight)
- **LLM/ML calls**: RPC plugins or Containers (need external API access)
- **Complex logic**: RPC plugins or Containers

**Considerations:**

- Enrichment often needs to be fast (inline processing)
- WASM good for sandboxed transformations
- RPC/Containers for external service calls

### Output Connectors

**Primary Recommendation: External Containers or HTTP/gRPC Services**

- Need to send data to external systems
- Independent scaling per destination
- Standard HTTP/gRPC clients
- Containers allow per-connector resource limits

**Alternative: RPC plugins**

- If tighter integration needed
- Lower latency than containers

## Input Connector Data Ingestion Patterns

Input connectors support two primary patterns for receiving data:

### 1. Polling Pattern (Active Fetch)

**Definition**: Connector actively fetches data from source at intervals

**Use Cases**:

- Services without webhook/message support (App Store, Play Store)
- Historical data backfill
- Scheduled data synchronization
- Services with rate-limited APIs
- Batch processing scenarios

**Implementation**:

- Hub maintains a scheduler/worker pool
- Each Input connector defines polling interval
- Connector `Poll()` method invoked at scheduled intervals
- Connector fetches data from source API
- Connector transforms and writes to Hub via internal API

**Pros**:

- Works with any API (no special requirements from source)
- Simple to implement
- Firewall-friendly (outbound only)
- Full control over fetch timing and rate limiting

**Cons**:

- Higher latency (polling interval)
- Wasted requests when no new data
- Rate limit concerns
- Resource usage (continuous polling)
- Not real-time

### 2. Message-Based Pattern (Passive Receive)

**Definition**: Connector receives data pushed from source via various message channels

#### 2a. Webhooks (Primary Message Source)

**Use Cases**:

- Real-time data ingestion (Formbricks, Typeform)
- Event-driven architectures
- Low-latency requirements
- Services with webhook support

**Implementation**:

- Hub exposes webhook endpoints per connector (e.g., `/webhooks/formbricks/{connector_id}`)
- External service sends HTTP POST to Hub webhook URL
- Hub validates webhook signature/authentication
- Hub routes to appropriate connector's `HandleWebhook()` method
- Connector processes webhook payload and writes to Hub

**Pros**:

- Real-time/low latency (sub-second)
- Efficient (only sends when data changes)
- No wasted requests
- Better for high-volume sources
- Standard HTTP protocol

**Cons**:

- Requires public endpoint (or webhook proxy service)
- Security concerns (validation, authentication, replay attacks)
- Retry logic needed for failures
- Some services don't support webhooks
- Network connectivity required

#### 2b. Message Queues (Future Enhancement)

**Use Cases**:

- High-volume event streaming
- Decoupled architectures
- Enterprise integrations with existing message infrastructure
- At-least-once delivery guarantees

**Supported Queue Types**:

- **Apache Kafka**: High-throughput event streaming
- **RabbitMQ**: AMQP-based message broker
- **AWS SQS**: Managed queue service
- **Redis Pub/Sub**: Lightweight pub/sub messaging
- **NATS**: Cloud-native messaging

**Implementation**:

- Connector subscribes to queue/topic
- Messages consumed and processed via `HandleMessage()` method
- Supports consumer groups for scaling
- Dead-letter queue support for failed messages

**Pros**:

- High throughput and scalability
- Decoupled architecture
- Built-in retry and dead-letter handling
- Supports multiple consumers
- Enterprise-grade reliability

**Cons**:

- Requires message queue infrastructure
- More complex setup and operations
- Additional infrastructure costs
- Learning curve for queue management

#### 2c. Server-Sent Events (SSE) (Future Enhancement)

**Use Cases**:

- Long-lived connections for streaming data
- Real-time dashboards and monitoring
- Services that support SSE (GitHub, etc.)

**Implementation**:

- Connector maintains SSE connection to source
- Streams events as they arrive
- Processes via `HandleSSEEvent()` method

**Pros**:

- Real-time streaming
- Efficient (single persistent connection)
- Standard HTTP protocol
- Automatic reconnection

**Cons**:

- Requires SSE support from source
- Connection management complexity
- Less common than webhooks

#### 2d. WebSocket Connections (Future Enhancement)

**Use Cases**:

- Bidirectional real-time communication
- Interactive data sources
- Custom protocols over WebSocket

**Implementation**:

- Connector maintains WebSocket connection
- Processes messages via `HandleWebSocketMessage()` method
- Supports bidirectional communication if needed

**Pros**:

- Real-time bidirectional communication
- Efficient persistent connection
- Standard protocol

**Cons**:

- Connection management complexity
- Less common for data ingestion
- Requires WebSocket support from source

#### 2e. Database Change Streams (Future Enhancement)

**Use Cases**:

- Capturing changes from databases
- Change Data Capture (CDC) patterns
- Real-time database replication

**Supported Sources**:

- **PostgreSQL Logical Replication**: WAL-based change streams
- **MongoDB Change Streams**: Document change events
- **MySQL Binlog**: Binary log replication

**Implementation**:

- Connector subscribes to database change stream
- Processes change events via `HandleChangeEvent()` method
- Transforms database changes to feedback records

**Pros**:

- Real-time database change capture
- No polling overhead
- Complete change history
- Standard CDC pattern

**Cons**:

- Database-specific implementations
- Requires replication permissions
- More complex setup

### Recommended Implementation Strategy

**Phase 1 (Initial)**:

- ✅ **Polling**: Core pattern for all Input connectors
- ✅ **Webhooks**: Primary message-based pattern (most common)

**Phase 2 (Future)**:

- Message Queues (Kafka, RabbitMQ, SQS) for high-volume scenarios
- SSE for streaming sources

**Phase 3 (Advanced)**:

- WebSocket for specialized use cases
- Database change streams for CDC patterns

### Connector Interface Design

Each Input connector declares supported patterns:

```go
type InputConnector interface {
    // Polling support (required for all)
    Poll(ctx context.Context) error

    // Message-based support (optional, connector-specific)
    HandleWebhook(ctx context.Context, payload []byte) error
    // Future: HandleMessage(ctx context.Context, msg QueueMessage) error
    // Future: HandleSSEEvent(ctx context.Context, event SSEEvent) error

    // Pattern declaration
    SupportedPatterns() []IngestionPattern
}

type IngestionPattern string

const (
    PatternPolling   IngestionPattern = "polling"
    PatternWebhook   IngestionPattern = "webhook"
    PatternQueue     IngestionPattern = "queue"
    PatternSSE       IngestionPattern = "sse"
    PatternWebSocket IngestionPattern = "websocket"
    PatternCDC       IngestionPattern = "cdc"
)
```

### Configuration Per Pattern

**Environment Variables (IMPLEMENTED)**:

```bash
# Formbricks Polling Connector
FORMBRICKS_URL=https://app.formbricks.com/api/v2
FORMBRICKS_POLLING_API_KEY=your-formbricks-api-key
FORMBRICKS_SURVEY_ID=your-survey-id

# Formbricks Webhook Connector
FORMBRICKS_WEBHOOK_API_KEY=your-webhook-secret-key
# Endpoint: POST /webhooks/formbricks?apiKey=<FORMBRICKS_WEBHOOK_API_KEY>
```

**Go Configuration (IMPLEMENTED)**:

```go
// Polling connector config (internal/connector/formbricks/connector.go)
type Config struct {
    URL             string
    APIKey          string
    SurveyID        string
    FeedbackService *service.FeedbackRecordsService
}

// Webhook connector config (internal/connector/formbricks/webhook_connector.go)
type WebhookConfig struct {
    FeedbackService *service.FeedbackRecordsService
}
```

## Architecture Considerations

### Hackathon Approach (Simplified)

**For 4-Day Hackathon: Start Simple, Prove Concept**

1. **Input Connectors**: Split into two types (trusted code for hackathon) - IMPLEMENTED
   - **PollingInputConnector**: Polling-based (Formbricks polling connector) - IMPLEMENTED
   - **WebhookInputConnector**: Webhook-based (Formbricks webhook connector) - IMPLEMENTED
   - In-process Go interfaces (fastest to implement)
   - Per-connector polling intervals (1 hour default)
   - Direct service access for writing feedback records
   - Can refactor to plugins/containers later

2. **Enrichment Connectors**: In-process Go interfaces
   - Simple transformations: Direct function calls (as efficient as possible)
   - LLM/ML/external APIs: HTTP client calls
   - Inline processing for low latency
   - Helper functions for Hub API access

3. **Output Connectors**: In-process Go interfaces
   - Event-driven processing
   - Helper functions for Hub API access
   - Can evolve to containers later

**Security Approach (Hackathon)**:

- Provide helper functions (`hubClient.CreateRecord()`, etc.) instead of direct API access
- In-process connectors are trusted for hackathon
- Design allows for WASM sandboxing later for untrusted community connectors

**Post-Hackathon: Hybrid Approach (Recommended)**

Different connector types may benefit from different approaches:

1. **Input Connectors**: RPC plugins (go-plugin) or Containers
   - Webhook handlers, API pollers
   - Process isolation important
   - Support both webhook and polling execution models

2. **Enrichment Connectors**: WASM for simple, RPC/Containers for complex
   - Simple transformations: WASM runtime
   - LLM/ML/external APIs: RPC plugins or Containers
   - Inline processing for low latency

3. **Output Connectors**: Containers or HTTP/gRPC services
   - Independent scaling
   - Standard HTTP clients

- Async processing acceptable

### AI-Assisted Connector Development

**Goal**: Enable 15-minute connector development for Enterprise custom integrations

### AI Code Generation Strategy

**Template-Based Generation**:

- Create connector templates for each type (Input/Enrichment/Output)
- Templates include standard patterns (API client, error handling, Hub integration)
- AI generates connector code from:
  - API documentation (OpenAPI/Swagger specs)
  - Example API responses
  - Destination schema requirements

**Key Enablers**:

1. **Standardized Interfaces**: Well-defined Go interfaces make generation predictable
2. **Code Templates**: Reusable templates for common patterns (OAuth, pagination, rate limiting)
3. **API Spec Parsing**: Parse OpenAPI specs to generate API clients
4. **Schema Mapping**: AI-assisted mapping from source to Hub data model
5. **Testing Templates**: Auto-generate tests for connectors

**Recommended Approach**:

- **RPC Plugins (go-plugin)**: Best for AI generation (standard Go code, well-understood patterns)
- **WASM**: Good for simple enrichment (smaller code surface, easier to generate)
- **Containers**: More complex but enables language-agnostic generation (Python, Node.js, etc.)

**Implementation**:

- Create connector generator tool/CLI
- Accept API specs, credentials, configuration
- Generate connector code, tests, and configuration
- Deploy to hub-playground for testing
- Refine based on usage patterns

## Implementation Phases

### 🚀 Hackathon MVP (Days 1-4)

**Day 1: Foundation & Architecture** ✅ COMPLETED

- ✅ Research connector approaches (this document)
- ✅ Define connector interfaces (Go interfaces for each connector type)
  - ✅ `PollingInputConnector` interface with `Poll()` method - `internal/connector/polling.go`
  - ✅ `WebhookInputConnector` interface with `HandleWebhook()` method - `internal/connector/webhook.go`
  - ⏳ `EnrichmentConnector` interface with `Enrich(data)` method (pending)
  - ⏳ `OutputConnector` interface with `HandleEvent(event)` method (pending)
- ✅ Implement basic connector infrastructure
  - ✅ `Poller` struct for managing polling connectors
  - ✅ `WebhookRouter` for managing webhook connectors with API key auth
  - ✅ Basic lifecycle management (start via context, stop via context cancellation)
  - ✅ Configuration via environment variables
- ✅ Formbricks SDK implemented - `pkg/formbricks/`

**Day 2: Core Infrastructure** ✅ COMPLETED

- ✅ Chose in-process Go interfaces (fastest to implement)
- ✅ Implement webhook endpoint routing (`POST /webhooks/{connector}?apiKey=<key>`)
  - ✅ `internal/api/handlers/webhook_handler.go` - HTTP handler
  - ✅ `internal/connector/webhook.go` - WebhookRouter with API key validation
- ✅ Implement basic polling scheduler (ticker-based)
  - ✅ `internal/connector/polling.go` - Poller with configurable interval
- ✅ Direct service access for connectors (FeedbackRecordsService)

**Day 3: Example Connectors** ✅ PARTIALLY COMPLETED

- ✅ Formbricks Polling Input connector - `internal/connector/formbricks/connector.go`
- ✅ Formbricks Webhook Input connector - `internal/connector/formbricks/webhook_connector.go`
- ✅ Data transformation layer - `internal/connector/formbricks/transformer.go`
- ⏳ OpenAI Sentiment Enrichment connector - **Priority #2** (pending)
- ⏳ Output connector (pending)

**Day 4: Polish & Documentation** ⏳ IN PROGRESS

- ✅ Integration tests for webhook endpoint - `tests/webhook_integration_test.go`
- ⏳ Document connector development patterns
- ⏳ Create README for hub-playground
- ⏳ Demo preparation

**Hackathon Deliverables**:

- ✅ Working connector system with 2-3 example connectors
- ✅ Event-driven output connector system
- ✅ Basic documentation on connector development
- ✅ Demo-ready implementation (can show end-to-end flow)
- ✅ Proof of concept for extensible connector architecture

**Risk Mitigation**:

- **If behind schedule**: Focus on Formbricks polling + Event system + Simple output logger (core demo)
  - Skip retry mechanism (can mention in presentation as future enhancement)
  - Skip webhook support (polling only is sufficient for demo)
- **If ahead of schedule**: Add webhook support, Typeform connector, enrichment pipeline, or basic retry mechanism
- **Critical path**: Day 1 connector + Day 2 event system = must work for demo
- **Retry mechanism**: Nice-to-have for hackathon, can be mentioned as planned feature

### 📋 Post-Hackathon Phases (Future Work)

**Phase 2: Production Hardening**

- Error handling and retry logic (see Retry Mechanism section below)
- Monitoring and observability
- Resource limits and quotas
- Hot-reloading support
- Webhook signature validation
- Rate limiting per connector
- Dead letter queue for failed events

**Phase 3: AI Generation Tools**

- Connector code generator CLI
- Template library for common patterns
- API spec parser (OpenAPI → connector code)
- Testing framework for generated connectors

**Phase 4: Multi-Approach Support**

- Add WASM runtime for simple enrichment
- Add container orchestration support
- Maintain consistent interface across approaches

**Phase 5: Advanced Message Patterns**

- Message queue support (Kafka, RabbitMQ, SQS)
- Server-Sent Events (SSE) support
- WebSocket connector support
- Database change stream connectors (PostgreSQL, MongoDB CDC)

**Phase 6: Enterprise Features**

- Connector marketplace/registry
- Enterprise connector management UI
- Webhook proxy service for private deployments
- Advanced monitoring and analytics

## Key Files to Create/Modify

### Hub Repository (`formbricks/hub`)

### Hackathon MVP Files (Days 1-4)

**Hub Repository (`formbricks/hub`)** - IMPLEMENTED FILES:

1. `internal/connector/` - Connector interfaces and infrastructure ✅
   - ✅ `polling.go` - `PollingInputConnector` interface and `Poller` struct
   - ✅ `webhook.go` - `WebhookInputConnector` interface and `WebhookRouter` struct
   - ⏳ `event_bus.go` - Event bus for output connectors (pending)
   - ⏳ `retry.go` - Retry mechanism (pending)

2. `internal/connector/formbricks/` - Formbricks connector implementations ✅
   - ✅ `connector.go` - Polling connector with `StartIfConfigured()`
   - ✅ `webhook_connector.go` - Webhook connector with `NewWebhookConnectorIfConfigured()`
   - ✅ `transformer.go` - Data transformation (polling and webhook payloads → feedback records)

3. `internal/api/handlers/webhook_handler.go` - Webhook endpoint handler ✅
   - ✅ Route webhooks to connectors (`POST /webhooks/{connector}?apiKey=<key>`)
   - ✅ API key validation via WebhookRouter
   - ✅ Error handling and logging

4. `pkg/formbricks/` - Formbricks SDK ✅
   - ✅ `client.go` - HTTP client for Formbricks API
   - ✅ `models.go` - Response, WebhookEvent, and related types
   - ✅ `responses.go` - GetResponses API method

5. `cmd/api/main.go` - Application wiring ✅
   - ✅ Initialize WebhookRouter
   - ✅ Register Formbricks webhook connector
   - ✅ Start Formbricks polling connector
   - ✅ Register webhook handler route

6. `tests/webhook_integration_test.go` - Integration tests ✅
   - ✅ Webhook endpoint validation tests
   - ✅ End-to-end feedback record creation tests

**Configuration** (`.env.example`):
```bash
# Formbricks Polling Connector
FORMBRICKS_URL=https://app.formbricks.com/api/v2
FORMBRICKS_POLLING_API_KEY=your-formbricks-api-key
FORMBRICKS_SURVEY_ID=your-survey-id

# Formbricks Webhook Connector
FORMBRICKS_WEBHOOK_API_KEY=your-webhook-secret-key
```

**Pending Files**:

- ⏳ `internal/connector/enrichment.go` - EnrichmentConnector interface
- ⏳ `internal/connector/output.go` - OutputConnector interface
- ⏳ `internal/connector/event_bus.go` - Event bus for output connectors
- ⏳ `docs/CONNECTORS.md` - Connector development guide

**Playground Repository (`formbricks/hub-playground`)** - PENDING:

1. ⏳ `connectors/formbricks/` - Formbricks Input connector (external version)
2. ⏳ `connectors/openai-sentiment/` - OpenAI Sentiment Enrichment connector
3. ⏳ `connectors/typeform/` - Typeform Input connector

### Post-Hackathon Files (Future)

4. `internal/connector/rpc/` - RPC plugin implementation (go-plugin)
   - `plugin.go` - RPC plugin loader and executor
   - `client.go` - gRPC client for plugin communication

5. `internal/connector/wasm/` - WASM runtime implementation (Extism)
   - `runtime.go` - WASM runtime wrapper
   - `host_functions.go` - Hub API access from WASM

6. `internal/connector/container/` - Container-based connector support
   - `orchestrator.go` - Container lifecycle management
   - `client.go` - HTTP/gRPC client for container connectors

### Playground Repository (`formbricks/hub-playground`)

1. `connectors/formbricks/` - Formbricks Input connector
   - `main.go` - Connector implementation
   - `config.go` - Configuration structure
   - `README.md` - Setup and usage

2. `connectors/typeform/` - Typeform Input connector
3. `connectors/openai-sentiment/` - OpenAI Sentiment Enrichment connector
4. `connectors/app-store/` - Apple App Store Input connector (polling)
5. `connectors/play-store/` - Google Play Store Input connector (polling)
6. `templates/` - Connector code templates for AI generation
7. `tools/generator/` - Connector code generator CLI

## Example Connector Specifications

### Formbricks Input Connector ✅ IMPLEMENTED

**Pattern**: Webhook + Polling (for backfill)

**Actual Implementation** (from `internal/connector/formbricks/`):

```go
// Polling Connector (connector.go)
type Connector struct {
    client          *formbricks.Client
    surveyID        string
    feedbackService *service.FeedbackRecordsService
}

// Implements PollingInputConnector interface
func (c *Connector) Poll(ctx context.Context) error {
    // Get responses from Formbricks API
    responses, err := c.client.GetResponses(formbricks.GetResponsesOptions{
        SurveyID: c.surveyID,
    })
    
    // Transform and create feedback records
    for _, response := range responses.Data {
        feedbackRecords := TransformResponseToFeedbackRecords(response)
        for _, recordReq := range feedbackRecords {
            c.feedbackService.CreateFeedbackRecord(ctx, recordReq)
        }
    }
    return nil
}

// Auto-start if configured
func StartIfConfigured(ctx context.Context, feedbackService *service.FeedbackRecordsService) {
    // Reads FORMBRICKS_POLLING_API_KEY, FORMBRICKS_SURVEY_ID, FORMBRICKS_URL
    // Creates connector and starts polling with 1-hour interval
}

// Webhook Connector (webhook_connector.go)
type WebhookConnector struct {
    feedbackService *service.FeedbackRecordsService
}

// Implements WebhookInputConnector interface
func (c *WebhookConnector) HandleWebhook(ctx context.Context, payload []byte) error {
    var event formbricks.WebhookEvent
    json.Unmarshal(payload, &event)
    
    // Handle responseCreated, responseUpdated, responseFinished events
    feedbackRecords := TransformWebhookToFeedbackRecords(event)
    for _, recordReq := range feedbackRecords {
        c.feedbackService.CreateFeedbackRecord(ctx, recordReq)
    }
    return nil
}

// Auto-configure if env var set
func NewWebhookConnectorIfConfigured(feedbackService) (*WebhookConnector, string) {
    // Reads FORMBRICKS_WEBHOOK_API_KEY
    // Returns connector and API key for registration
}
```

**Configuration** (Environment Variables):

```bash
# Polling Connector
FORMBRICKS_URL=https://app.formbricks.com/api/v2  # Optional, has default
FORMBRICKS_POLLING_API_KEY=your-api-key           # Required for polling
FORMBRICKS_SURVEY_ID=your-survey-id               # Required for polling

# Webhook Connector
FORMBRICKS_WEBHOOK_API_KEY=your-webhook-secret    # Required for webhooks
# Endpoint: POST /webhooks/formbricks?apiKey=<FORMBRICKS_WEBHOOK_API_KEY>
```

**Data Transformation** (`transformer.go`):

- `TransformResponseToFeedbackRecords()` - For polling responses
- `TransformWebhookToFeedbackRecords()` - For webhook events
- Extracts each data field as a separate feedback record
- Includes metadata: survey info, response timestamps, user agent, etc.

### OpenAI Sentiment Enrichment Connector

**Pattern**: Inline processing (WASM or RPC)

**Implementation**:

```go
type OpenAISentimentConnector struct {
    config EnrichmentConnectorConfig
    // ... internal state
}

func (c *OpenAISentimentConnector) Enrich(ctx context.Context, record *FeedbackRecord) (*FeedbackRecord, error) {
    // Extract text from record
    // Call OpenAI API for sentiment analysis
    // Add sentiment metadata to record
    // Return enriched record
}

func (c *OpenAISentimentConnector) GetConfig() EnrichmentConnectorConfig {
    return c.config
}

// Registration
RegisterEnrichmentConnector("openai-sentiment", &OpenAISentimentConnector{}, EnrichmentConnectorConfig{
    APIKey: "...",
    Model: "gpt-4",
})
```

**Configuration**:

- OpenAI API key
- Model selection
- Metadata field names

## Architecture Decisions & Requirements

**Deployment Environment**: Not relevant for hackathon (architecture should be flexible for future deployment options)

**Connector Source**: Both Formbricks team and community will develop connectors

- Architecture must support easy connector development
- Clear interfaces and documentation essential
- Community-friendly patterns (simple, well-documented)

**Security Requirements**: As safe as possible

- Provide helper functions for connectors to use (limited API access)
- Sandboxing important for untrusted code (WASM good fit for this)
- In-process connectors (hackathon) are trusted, but design should allow sandboxing later
- Helper functions pattern: `hubClient.CreateRecord()`, `hubClient.GetRecord()`, etc.

**Performance Targets**: As efficient as possible

- Enrichment connectors should be fast (inline processing preferred)
- Minimize latency in data pipeline
- Efficient event routing for output connectors

**Operational Complexity**: As simple as possible

- Prefer in-process interfaces over containers for hackathon
- Keep deployment and management simple
- Can evolve to containers/plugins later if needed

**AI Generation Priority**: Manual examples first

- Build working examples to understand patterns
- Document patterns for future AI generation
- Templates can be created after manual examples prove the concept

**Webhook Infrastructure**: Expose webhook endpoints directly, but keep architecture flexible

- Hub will expose webhook endpoints (`/webhooks/{connector_name}`)
- Design allows for webhook proxy service in future if needed
- Keep webhook routing flexible

**Polling Strategy**: Per connector configuration

- Each connector defines its own polling interval
- No global polling strategy
- Connectors can have different intervals based on source API rate limits

## Retry Mechanism for Error Handling

**Requirement**: Implement retry logic for connector failures to ensure reliability

### Retry Strategy

**When to Retry**:

- Network errors (timeouts, connection failures)
- Transient API errors (5xx status codes, rate limits)
- Temporary service unavailability
- Event processing failures (output connectors)

**When NOT to Retry**:

- Authentication errors (4xx with auth issues)
- Validation errors (malformed data)
- Permanent failures (4xx client errors, except rate limits)

### Implementation Approach

**Hackathon (Basic)**:

- Simple retry wrapper with exponential backoff
- Configurable per connector type
- Basic error classification (retryable vs non-retryable)
- Log retry attempts

**Post-Hackathon (Production)**:

- Dead letter queue for failed events
- Retry with exponential backoff and jitter
- Circuit breaker pattern for repeated failures
- Metrics and monitoring for retry rates
- Configurable retry policies per connector

### Retry Configuration

```go
type RetryConfig struct {
    MaxRetries      int           // Default: 3
    InitialDelay    time.Duration // Default: 1 second
    MaxDelay        time.Duration // Default: 60 seconds
    BackoffFactor   float64       // Default: 2.0 (exponential)
    RetryableErrors []error       // Custom retryable errors
}

// Per-connector retry config (future enhancement)
type PollingInputConnectorConfig struct {
    // ... existing fields
    RetryConfig *RetryConfig  // Optional, uses defaults if nil
}

type WebhookInputConnectorConfig struct {
    // ... existing fields
    RetryConfig *RetryConfig
}

type EnrichmentConnectorConfig struct {
    // ... existing fields
    RetryConfig *RetryConfig
}

type OutputConnectorConfig struct {
    // ... existing fields
    RetryConfig *RetryConfig
}
```

### Retry Implementation

```go
// Retry wrapper for connector operations
func RetryWithBackoff(ctx context.Context, config RetryConfig, operation func() error) error {
    var lastErr error
    delay := config.InitialDelay

    for attempt := 0; attempt <= config.MaxRetries; attempt++ {
        if attempt > 0 {
            // Wait before retry
            select {
            case <-ctx.Done():
                return ctx.Err()
            case <-time.After(delay):
            }
            delay = time.Duration(float64(delay) * config.BackoffFactor)
            if delay > config.MaxDelay {
                delay = config.MaxDelay
            }
        }

        err := operation()
        if err == nil {
            return nil
        }

        // Check if error is retryable
        if !isRetryableError(err, config) {
            return err
        }

        lastErr = err
        slog.Warn("Connector operation failed, will retry",
            "attempt", attempt+1,
            "max_retries", config.MaxRetries,
            "error", err,
        )
    }

    return fmt.Errorf("operation failed after %d retries: %w", config.MaxRetries, lastErr)
}

func isRetryableError(err error, config RetryConfig) bool {
    // Check custom retryable errors
    for _, retryableErr := range config.RetryableErrors {
        if errors.Is(err, retryableErr) {
            return true
        }
    }

    // Default: retry on network/timeout errors, 5xx, rate limits
    // Don't retry on 4xx (except rate limits), validation errors
    // Implementation details...
    return true // Simplified for hackathon
}
```

### Usage in Connectors (Future Enhancement)

**Polling Input Connector**:

```go
func (m *ConnectorManager) executePollingConnector(ctx context.Context, connector PollingInputConnector) error {
    config := m.getConnectorConfig(connector.GetName())
    retryConfig := config.RetryConfig
    if retryConfig == nil {
        retryConfig = DefaultRetryConfig()
    }

    return RetryWithBackoff(ctx, *retryConfig, func() error {
        return connector.Poll(ctx)
    })
}
```

**Webhook Input Connector**:

```go
func (m *ConnectorManager) executeWebhookConnector(ctx context.Context, connector WebhookInputConnector, payload []byte) error {
    config := m.getConnectorConfig(connector.GetName())
    retryConfig := config.RetryConfig
    if retryConfig == nil {
        retryConfig = DefaultRetryConfig()
    }

    return RetryWithBackoff(ctx, *retryConfig, func() error {
        return connector.HandleWebhook(ctx, payload)
    })
}
```

**Output Connector**:

```go
func (m *ConnectorManager) executeOutputConnector(ctx context.Context, connector OutputConnector, event FeedbackRecordEvent) error {
    config := m.getConnectorConfig(connector.GetName())
    retryConfig := config.RetryConfig
    if retryConfig == nil {
        retryConfig = DefaultRetryConfig()
    }

    return RetryWithBackoff(ctx, *retryConfig, func() error {
        return connector.HandleEvent(ctx, event)
    })
}
```

### Hackathon Implementation Priority

**Day 2 (If Time Permits)**:

- Basic retry wrapper with fixed retry count (3 attempts)
- Simple exponential backoff
- Basic error logging

**Day 3-4 (Polish)**:

- Configurable retry per connector
- Better error classification
- Retry metrics/logging

**Post-Hackathon**:

- Dead letter queue
- Circuit breaker pattern
- Advanced retry policies
- Monitoring and alerting
