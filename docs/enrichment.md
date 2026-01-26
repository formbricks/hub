# AI Enrichment Architecture

This document describes the AI enrichment system for Formbricks Hub, including Knowledge Records and Topics (taxonomy) that enable intelligent feedback categorization and insights.

## Overview

The Formbricks Hub stores feedback records from various sources. To provide meaningful AI-powered insights, we need two additional data types:

1. **Knowledge Records** - Contextual information about the product, company, and domain that helps AI understand feedback better
2. **Topics** - A hierarchical taxonomy for categorizing feedback into themes and subthemes

Together, these enable the AI to:
- Understand the context of feedback (what product features exist, company terminology, etc.)
- Categorize feedback into meaningful, user-defined categories
- Generate insights based on both the feedback content and organizational knowledge

## Knowledge Records

### Purpose

Knowledge Records store contextual information that enriches AI understanding when processing feedback. This could include:

- Product descriptions and feature explanations
- Company terminology and domain-specific language
- Business context and priorities
- Historical context about features or decisions

### Data Model

```
KnowledgeRecord {
  id: UUIDv7
  content: string (max 10,000 chars)
  tenant_id: string (optional, for multi-tenancy)
  created_at: datetime
  updated_at: datetime
}
```

### Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Content max length | 10,000 characters | Optimal for vector embeddings; longer content should be chunked |
| Fields | Minimal (id, content, timestamps) | MVP simplicity; can extend later with title, source_type, metadata |
| Bulk delete | By tenant_id | Supports data isolation and cleanup for multi-tenant deployments |

### Future Considerations

- **Vector embeddings**: Knowledge records will be embedded and stored in pgvector for semantic search
- **Chunking**: Long documents may need automatic chunking for optimal embedding performance
- **Linking**: May link knowledge records to specific topics or feedback categories

## Topics (Taxonomy)

### Purpose

Topics provide a hierarchical classification system for organizing feedback. Users define their own taxonomy based on their product and business needs.

**Example hierarchy:**
```
Performance (level 1)
├── Dashboard slow (level 2)
├── API response time (level 2)
└── Mobile app lag (level 2)

Feature Requests (level 1)
├── Dashboard improvements (level 2)
├── New integrations (level 2)
└── Mobile features (level 2)
```

### Data Model

```
Topic {
  id: UUIDv7
  title: string (max 255 chars)
  level: integer (auto-calculated)
  parent_id: uuid (nullable)
  tenant_id: string (optional)
  created_at: datetime
  updated_at: datetime
}
```

### Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Naming | "Topics" | Clear, concise, fits theme/subtheme concept better than "taxonomy-items" or "categories" |
| Level field | Auto-calculated by server | Prevents inconsistencies; server computes as `parent.level + 1` or `1` if no parent |
| Level support | Unlimited depth (designed for 3-4+ levels) | Future-proofed for complex taxonomies |
| Parent immutability | Cannot change parent_id after creation | Simplifies hierarchy management, prevents circular references |
| Title uniqueness | Unique within same parent + tenant | Prevents confusing duplicate siblings |
| Delete behavior | Cascade delete | Deleting a parent removes all descendants |

### Why These Constraints?

**Level auto-calculation**: Allowing manual level input creates risk of inconsistent hierarchies (e.g., a child with level 1 under a parent with level 2). Server-side calculation ensures data integrity.

**Parent immutability**: Moving topics between parents would require:
- Recalculating levels for entire subtrees
- Handling circular reference detection
- Complex UI for tree restructuring

For MVP, immutability is simpler and safer. Users can delete and recreate topics if restructuring is needed.

**Cascade delete**: Alternative approaches considered:
- **Prevent deletion** if children exist (safest but restrictive)
- **Orphan children** by setting parent_id to null (confusing UX)
- **Cascade delete** (chosen) - clear behavior, documented in API

## API Design

### Endpoints

#### Knowledge Records
- `GET /v1/knowledge-records` - List with filters
- `POST /v1/knowledge-records` - Create
- `GET /v1/knowledge-records/{id}` - Get by ID
- `PATCH /v1/knowledge-records/{id}` - Update content
- `DELETE /v1/knowledge-records/{id}` - Delete single
- `DELETE /v1/knowledge-records?tenant_id=...` - Bulk delete by tenant

#### Topics
- `GET /v1/topics` - List with filters (level, parent_id, title, tenant_id)
- `POST /v1/topics` - Create (level auto-calculated)
- `GET /v1/topics/{id}` - Get by ID
- `PATCH /v1/topics/{id}` - Update title only
- `DELETE /v1/topics/{id}` - Delete (cascades to descendants)

### Authentication

All endpoints are protected by the same `ApiKeyAuth` (Bearer token) used for feedback records. No additional configuration needed - inherited from global security scheme.

### Multi-tenancy

Both Knowledge Records and Topics support optional `tenant_id` for multi-tenant deployments. This allows:
- Different tenants to have separate knowledge bases
- Different tenants to have separate taxonomies
- Data isolation and independent bulk operations

### Error Handling

Following RFC 7807 Problem Details (`application/problem+json`):

| Status | When |
|--------|------|
| 400 Bad Request | Validation failures, missing required fields |
| 404 Not Found | Record/topic not found, parent topic not found |
| 409 Conflict | Duplicate topic title within same parent and tenant |

## Future Roadmap

### Phase 1: MVP (Current)
- [x] Knowledge Records CRUD API
- [x] Topics CRUD API with hierarchy
- [ ] Database schema and migrations
- [ ] Service and repository implementation
- [ ] Integration tests

### Phase 2: Vector Search
- [ ] pgvector integration for knowledge records
- [ ] Embedding generation pipeline
- [ ] Semantic search endpoints
- [ ] Knowledge record chunking for long content

### Phase 3: AI Categorization
- [ ] Link feedback records to topics
- [ ] AI-powered automatic categorization
- [ ] Confidence scores for categorizations
- [ ] Manual override and feedback loop

### Phase 4: Insights
- [ ] Topic-based aggregations
- [ ] Trend detection within topics
- [ ] Knowledge-enhanced insight generation
- [ ] Dashboard visualizations

## Technical Notes

### Validation Patterns

Following existing feedback-records patterns:
- NULL bytes pattern: `^[^\x00]*$` on all text fields
- String length constraints: `minLength`, `maxLength`
- UUID format validation for IDs
- Pagination: limit (1-1000, default 100), offset (0-2147483647)

### Naming Conventions

| Concept | Naming |
|---------|--------|
| Resource URLs | kebab-case plural (`/v1/knowledge-records`, `/v1/topics`) |
| JSON fields | snake_case (`tenant_id`, `parent_id`, `created_at`) |
| Schema names | PascalCase (`KnowledgeRecordData`, `TopicData`) |
| Operation IDs | kebab-case verb-noun (`list-topics`, `create-knowledge-record`) |

---

*Last updated: January 2026*
*Status: OpenAPI spec complete, implementation pending*
