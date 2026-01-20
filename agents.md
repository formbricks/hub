# AI Agents & Assistants

This document describes the AI agents, assistants, and AI-powered features used in the Formbricks Hub project.

## Overview

Formbricks Hub is designed with AI-powered capabilities in mind. While the current implementation focuses on core CRUD operations and production readiness, the architecture is designed to support future AI-powered features for intelligent data processing, connector management, and analytics.

## Current AI Integration

### Development Assistants

The project uses AI development assistants (like Cursor's AI) for:
- Code generation and refactoring
- Documentation generation
- Implementation planning
- Code review and optimization

## Planned AI Features

### 1. AI-Powered Connector Framework

**Status**: Planned (Post-Parity)

**Purpose**: Automatically discover, map, and normalize data from diverse sources using AI.

**Key Capabilities**:
- **Connector Discovery**: AI analyzes incoming data to understand structure and semantic meaning
- **Automatic Mapping**: AI maps source fields to unified experience data model based on semantic understanding
- **Mapping Maintenance**: AI detects schema changes and updates mappings automatically
- **Taxonomy Application**: Auto-apply consistent taxonomy across sources
- **Context Enrichment**: AI adds contextual metadata for better analytics

**Architecture**:
- Connector interface and abstracted broker layer
- AI mapping service for semantic schema discovery
- Mapping storage and versioning system
- Human-in-the-loop validation for critical mappings
- Feedback loop for continuous AI model improvement

**Key Insight**: The hardest part of connectors is **semantic data context**, not technical connectivity. Field names often lack clear context - AI must understand meanings, not just API structure.

### 2. AI Enrichment (Sentiment, Emotion, Topics)

**Status**: Planned (Deferred)

**Purpose**: Automatically enrich feedback records with sentiment analysis, emotion detection, and topic extraction.

**Features**:
- Sentiment analysis with quantifiable scores
- Emotion detection
- Topic extraction and classification
- Multi-language support
- Historical data handling policies

**Implementation**:
- Async job queue for enrichment processing
- Background workers for processing
- OpenAI API integration (or similar LLM service)
- Configurable enrichment triggers

**Note**: This feature is deferred until core CRUD operations are complete and the AI approach is determined.

### 3. Semantic Search

**Status**: Planned (Deferred)

**Purpose**: Enable semantic search across feedback records using vector embeddings.

**Features**:
- Vector embeddings generation (OpenAI or similar)
- Semantic similarity search using pgvector
- Fallback to full-text search
- Query understanding and intent recognition

**Implementation**:
- pgvector extension for PostgreSQL
- Embedding generation service
- Vector similarity search endpoint
- Embedding model versioning

**Note**: This feature is deferred until search functionality is needed. Focus is on full CRUD operations first.

## AI Architecture Principles

### Design Principles

1. **AI-First Approach**: Use AI to reduce manual maintenance and understand semantic context
2. **Foundation Framework**: Build foundational framework and tooling, not individual connectors
3. **Progressive Enhancement**: Start with AI suggestions, allow human refinement
4. **Feedback Loops**: Track good vs. bad AI suggestions for continuous model improvement
5. **Human-in-the-Loop**: Critical mappings require human validation
6. **Cost Efficiency**: Consider AI API costs and optimize for production use

### Key Challenges Addressed

**Connector Ecosystem Maintenance**:
- Traditional approach: 200-400 connectors = unsustainable maintenance burden
- AI approach: Intelligent framework reduces manual maintenance
- Semantic context mapping is harder than technical connectivity - this is where AI provides value

**Data Context Understanding**:
- Field names often lack clear context
- AI must understand semantic meanings, not just data structure
- Support for multiple languages and phrase variations

**Scale & Performance**:
- AI processing must not block high-throughput data ingestion
- Async processing for enrichment jobs
- Efficient embedding generation and storage

## Future AI Capabilities

### Intelligent Data Normalization

- Automatic field type detection
- Value normalization across sources
- Duplicate detection and merging

### Predictive Analytics

- Trend detection
- Anomaly detection
- Predictive insights based on historical patterns

### Natural Language Processing

- Query understanding for search
- Intent recognition
- Multi-language support

## Integration Points

### Current Architecture Support

The current architecture is designed to support AI integration:

- **Service Layer**: Business logic layer can integrate AI services
- **Repository Layer**: Data access layer supports additional fields (enrichment, embeddings)
- **Async Processing**: Architecture supports background workers for AI processing
- **Webhook System**: AI-enriched events can be sent via webhooks
- **Extensible Models**: Data models can be extended with AI-generated fields

### Planned Integration Points

1. **Enrichment Service**: `internal/enrichment/service.go`
2. **Connector Framework**: `internal/connector/` (future)
3. **AI Mapping Service**: `internal/ai/mapping.go` (future)
4. **Embedding Service**: `internal/embedding/service.go` (future)
5. **Job Queue**: `internal/queue/` (future)

## Research & Insights

### Key Research Findings

**Connector Ecosystem Reality**:
- Customers don't want to develop - want out-of-the-box solutions
- 200-400 connectors is already a maintenance nightmare
- AI/MCP server route is a potential solution
- Semantic data context is the hardest part, not technical connectivity

**Technical Challenges**:
- Field names often lack clear context
- Requirements change constantly
- APIs change on the other side
- Engineering teams need extensive time to learn each system

**Solution Direction**:
- AI-powered connector discovery and mapping
- Build foundational framework, not 100 individual connectors
- Use AI/MCP for intelligent connector generation
- Focus on semantic understanding, not just API connectivity

## Configuration

Future AI features will be configured via environment variables:

```bash
# AI Enrichment
OPENAI_API_KEY=...
ENRICHMENT_ENABLED=true
ENRICHMENT_MODEL=gpt-4

# Semantic Search
EMBEDDING_MODEL=text-embedding-3-large
VECTOR_DIMENSION=1536

# AI Mapping
AI_MAPPING_ENABLED=true
AI_MAPPING_MODEL=gpt-4
```

## Documentation

- [Implementation Plan](.cursor/plans/feature_parity_implementation_plan_766455a7.plan.md) - See "Future: AI-Powered Connector Architecture" section
- [README](README.md) - Project overview and architecture

## Notes

- AI features are planned but not yet implemented
- Current focus is on core CRUD operations and production readiness
- Architecture is designed to support future AI integration
- AI approach will be determined based on production requirements and cost considerations
