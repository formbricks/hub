---
sidebar_position: 1
---

# Data Model

Hub uses a simple, powerful data model optimized for analytics. Each record represents **one answer to one question**, making it incredibly easy to query, aggregate, and visualize your experience data.

## How It Works

Every piece of feedback—whether it's an NPS score, a text comment, or a multiple-choice selection—is stored as an individual record. This "one row per response" approach unlocks powerful capabilities:

✅ **Query with standard SQL** - No complex JSON parsing or unnesting required  
✅ **Connect any BI tool** - Works seamlessly with Apache Superset, Power BI, Tableau, Looker  
✅ **Combine data sources** - Mix surveys, reviews, support tickets in a single query  
✅ **Analyze trends over time** - Built-in time-series indexing for fast date-based queries  
✅ **AI-enriched insights** - Automatic sentiment, emotion, and topic extraction for text feedback

### Example: Survey Response

When a user completes a survey with an NPS score and a comment, Hub creates two records:

```json
// Record 1: NPS score
{
  "source_id": "nps-2024",
  "field_id": "nps_score",
  "field_type": "nps",
  "value_number": 9
}

// Record 2: Comment with AI enrichment
{
  "source_id": "nps-2024",
  "field_id": "feedback_comment",
  "field_type": "text",
  "value_text": "Great service!",
  "sentiment": "positive",
  "emotion": "joy",
  "topics": ["service_quality"]
}
```

This structure makes it trivial to calculate your NPS score or find all negative sentiment across any source.

## Core Entity: ExperienceData

The `ExperienceData` entity stores individual experience data points with optional AI enrichment for
qualitative feedback.

### Complete Field Reference

#### Core Identification

| Field              | Type      | Required | Description                                                  |
| ------------------ | --------- | -------- | ------------------------------------------------------------ |
| **`id`**           | UUIDv7    | Auto     | Time-ordered primary key for efficient indexing              |
| **`collected_at`** | Timestamp | ✅       | When the feedback was originally collected (defaults to now) |
| **`created_at`**   | Timestamp | Auto     | When the record was created in the Hub                     |
| **`updated_at`**   | Timestamp | Auto     | When the record was last updated                             |

#### Multi-Tenancy & Response Grouping

| Field           | Type   | Required | Description                                                         |
| --------------- | ------ | -------- | ------------------------------------------------------------------- |
| `tenant_id`     | String | Optional | Tenant/organization identifier for multi-tenant deployments         |
| `response_id`   | String | Optional | Groups multiple answers from a single submission/session            |

:::tip Response Grouping
Use `response_id` to link all answers from a single survey submission. This enables:
- Querying complete responses: `WHERE response_id = 'resp-abc-123'`
- Calculating completion rates
- GDPR compliance: delete all answers from one submission together
- Response-level analysis across multiple questions
:::

#### Source Tracking

| Field             | Type   | Required | Description                                          |
| ----------------- | ------ | -------- | ---------------------------------------------------- |
| **`source_type`** | String | ✅       | Type of source (e.g., "survey", "review", "support") |
| `source_id`       | String | Optional | Reference to survey/form/ticket/review ID            |
| `source_name`     | String | Optional | Human-readable source name for display               |

#### Question/Field Identification

| Field            | Type   | Required | Description                                                  |
| ---------------- | ------ | -------- | ------------------------------------------------------------ |
| **`field_id`**   | String | ✅       | Unique question/field identifier (stable across submissions) |
| `field_label`    | String | Optional | Question text or field label for display                     |
| **`field_type`** | Enum   | ✅       | Data type (see [Field Types](#field-types) below)            |

#### Response Values

| Field           | Type      | Required | Description                                             |
| --------------- | --------- | -------- | ------------------------------------------------------- |
| `value_text`    | String    | Optional | Text responses (feedback, comments, open-ended answers) |
| `value_number`  | Float64   | Optional | Numeric responses (ratings, scores, NPS, CSAT)          |
| `value_boolean` | Boolean   | Optional | Yes/no responses                                        |
| `value_date`    | Timestamp | Optional | Date/datetime responses                                 |

#### AI Enrichment (Automatic for `text` field types)

| Field             | Type     | Required | Description                                                         |
| ----------------- | -------- | -------- | ------------------------------------------------------------------- |
| `sentiment`       | String   | Auto     | Sentiment analysis: "positive", "negative", "neutral", "mixed"      |
| `sentiment_score` | Float64  | Auto     | Sentiment confidence score: -1.0 (negative) to 1.0 (positive)       |
| `emotion`         | String   | Auto     | Primary emotion: "joy", "frustration", "anger", "confusion", etc.   |
| `topics`          | String[] | Auto     | Extracted topics/themes (e.g., ["pricing", "ui_design", "support"]) |

#### Context & Metadata

| Field             | Type   | Required | Description                                                  |
| ----------------- | ------ | -------- | ------------------------------------------------------------ |
| `metadata`        | JSONB  | Optional | Flexible context (device, location, campaign, custom fields) |
| `language`        | String | Optional | ISO 639-1 language code (e.g., "en", "de", "fr")             |
| `user_identifier` | String | Optional | Anonymous user ID for tracking (hashed, never PII)           |

### Field Types

The `field_type` field uses **validated enums** to categorize responses and determine which
`value_*` field to use. Hub enforces these 9 standardized types optimized for analytics:

| Type              | Description                        | Value Field     | AI Enrichment | Analytics Use                                    |
| ----------------- | ---------------------------------- | --------------- | ------------- | ------------------------------------------------ |
| **`text`**        | Open-ended qualitative feedback    | `value_text`    | ✅ **Yes**    | Sentiment trends, topic analysis, word clouds    |
| **`categorical`** | Pre-defined discrete options       | `value_text`    | ❌ No         | Frequency distribution, most popular choices     |
| **`nps`**         | Net Promoter Score (0-10)          | `value_number`  | ❌ No         | NPS calculation, promoter/detractor segmentation |
| **`csat`**        | Customer Satisfaction (1-5 or 1-7) | `value_number`  | ❌ No         | Average satisfaction, benchmarking               |
| **`ces`**         | Customer Effort Score (1-7)        | `value_number`  | ❌ No         | Effort analysis, ease-of-use tracking            |
| **`rating`**      | Generic rating scale               | `value_number`  | ❌ No         | Average rating, distribution, star ratings       |
| **`number`**      | Quantitative measurements          | `value_number`  | ❌ No         | Sum, average, min/max, aggregations              |
| **`boolean`**     | Binary yes/no responses            | `value_boolean` | ❌ No         | True/false counts, percentages                   |
| **`date`**        | Temporal values                    | `value_date`    | ❌ No         | Time-series analysis, date filtering             |

:::tip AI Enrichment
Only `text` field types are automatically enriched with AI-powered sentiment, emotion, and topic extraction. This happens asynchronously after data is saved, optimizing costs and performance. [Learn more about AI enrichment →](./ai-enrichment)
:::

:::info Why These 9 Types?
These types cover 95% of experience management use cases while keeping the model simple. They map directly to common analytics patterns:

- **Qualitative**: `text` → AI analysis for themes and sentiment
- **Quantitative**: `nps`, `csat`, `ces`, `rating`, `number` → Statistical aggregations
- **Categorical**: `categorical` → Frequency and distribution analysis
- **Binary/Temporal**: `boolean`, `date` → Filtering and segmentation
:::

### Source Types

The `source_type` field identifies where the experience data originated:

| Type             | Description             | Typical Use                        |
| ---------------- | ----------------------- | ---------------------------------- |
| `survey`         | Survey responses        | General surveys                    |
| `nps_campaign`   | NPS campaigns           | Net Promoter Score tracking        |
| `review`         | Product/service reviews | App Store, Google Play, Trustpilot |
| `feedback_form`  | General feedback        | Contact forms, feedback widgets    |
| `support`        | Support tickets         | Zendesk, Intercom satisfaction     |
| `social`         | Social media            | Twitter mentions, Reddit posts     |
| `interview`      | User interviews         | Qualitative research               |
| `usability_test` | UX testing              | Task completion feedback           |

:::tip Custom Source Types
Source types are free-form strings. Define your own like `app_store_review`, `zendesk_csat`, or `slack_pulse` to match your data sources.
:::

## Example Records

### NPS Response

```json
{
  "source_type": "survey",
  "source_id": "q1-2025-nps",
  "source_name": "Q1 2025 Customer NPS",
  "field_id": "nps_score",
  "field_label": "How likely are you to recommend us?",
  "field_type": "nps",
  "value_number": 9,
  "metadata": {
    "campaign_id": "email-blast-001",
    "customer_segment": "enterprise",
    "country": "US"
  },
  "user_identifier": "user-abc-123",
  "collected_at": "2025-01-15T10:30:00Z"
}
```

### Text Response with AI Enrichment

```json
{
  "source_type": "survey",
  "source_id": "onboarding-survey-v2",
  "field_id": "q3_improvements",
  "field_label": "What could we improve?",
  "field_type": "text",
  "value_text": "The dashboard is confusing for new users, but the support team was very helpful!",
  "language": "en",
  "sentiment": "mixed",
  "sentiment_score": 0.2,
  "emotion": "hopeful",
  "topics": ["ui_design", "customer_support", "onboarding"],
  "metadata": {
    "device": "mobile",
    "app_version": "2.1.4"
  },
  "user_identifier": "user-xyz-789",
  "collected_at": "2025-01-15T10:32:15Z"
}
```

### G2 Review (Rating + Text)

A single G2 review creates **two rows** - one for the rating, one for the review text:

```json
// Row 1: Rating
{
  "source_type": "review",
  "source_id": "g2-review-12345",
  "source_name": "G2 Reviews",
  "field_id": "overall_rating",
  "field_label": "Overall Rating",
  "field_type": "rating",
  "value_number": 4.5,
  "metadata": {
    "platform": "g2",
    "product": "formbricks",
    "reviewer_title": "Product Manager"
  },
  "collected_at": "2025-01-10T08:00:00Z"
}

// Row 2: Review text (AI enriched)
{
  "source_type": "review",
  "source_id": "g2-review-12345",
  "source_name": "G2 Reviews",
  "field_id": "review_text",
  "field_label": "Review",
  "field_type": "text",
  "value_text": "Great open-source tool! The privacy features are excellent, though the documentation could be more detailed.",
  "sentiment": "positive",
  "sentiment_score": 0.7,
  "emotion": "satisfied",
  "topics": ["open_source", "privacy", "documentation"],
  "metadata": {
    "platform": "g2",
    "product": "formbricks",
    "reviewer_title": "Product Manager"
  },
  "collected_at": "2025-01-10T08:00:00Z"
}
```

### Multiple Choice Response (Multiple Rows)

When a user selects multiple options, each choice becomes a **separate row**:

```json
// User selected "Dashboards", "Reports", and "Alerts"

// Row 1
{
  "source_type": "survey",
  "source_id": "product-feedback-2025",
  "field_id": "features_used",
  "field_label": "Which features do you use?",
  "field_type": "categorical",
  "value_text": "Dashboards",
  "user_identifier": "user-qwe-456",
  "collected_at": "2025-01-12T14:22:00Z"
}

// Row 2
{
  "source_type": "survey",
  "source_id": "product-feedback-2025",
  "field_id": "features_used",
  "field_label": "Which features do you use?",
  "field_type": "categorical",
  "value_text": "Reports",
  "user_identifier": "user-qwe-456",
  "collected_at": "2025-01-12T14:22:00Z"
}

// Row 3
{
  "source_type": "survey",
  "source_id": "product-feedback-2025",
  "field_id": "features_used",
  "field_label": "Which features do you use?",
  "field_type": "categorical",
  "value_text": "Alerts",
  "user_identifier": "user-qwe-456",
  "collected_at": "2025-01-12T14:22:00Z"
}
```

:::tip Multi-Row Responses
This "one row per selection" approach makes it trivial to count how many users selected each feature:

```sql
SELECT value_text, COUNT(*) as user_count
FROM experience_data
WHERE field_id = 'features_used'
GROUP BY value_text;
```
:::

## Database Indexes

Hub automatically creates indexes for optimal query performance:

- **`source_type`** - Filter by feedback source (survey, review, support)
- **`source_id`** - Query specific surveys/forms
- **`collected_at`** - Time-series queries and trending
- **`field_type`** - Filter by question type
- **`field_id`** - Group related questions across responses
- **`value_number`** - Numeric aggregations (averages, sums, counts)
- **`user_identifier`** - User-level journey analysis
- **`sentiment`** - Filter by sentiment for AI-enriched text
- **`emotion`** - Filter by emotion for qualitative analysis

:::tip Query Performance
All indexes are created automatically via database migrations. The combination of UUIDv7 primary keys and strategic indexes ensures fast queries even with millions of records.
:::

## UUIDv7 Primary Keys

Hub uses **UUIDv7** for primary keys, combining the benefits of UUIDs with time-ordered sorting:

**What you get:**
- ✅ **Chronological sorting** - IDs sort by creation time automatically
- ✅ **Fast database performance** - Better B-tree indexing than random UUIDs
- ✅ **Globally unique** - No collision risk across distributed systems
- ✅ **Horizontally scalable** - No need for central ID generation

**Example UUIDv7:**
```
01932c8a-8b9e-7000-8000-000000000001
└─ timestamp ─┘ └── random ──┘
```

The timestamp prefix means newer records naturally have larger IDs, improving both query performance and ordering.

## JSONB Metadata

Hub uses PostgreSQL's `JSONB` type for flexible contextual data storage in the `metadata` field.
This allows you to attach any custom attributes to your experience data without schema changes.

### Common Metadata Patterns

```json
{
  "metadata": {
    // Device & Platform
    "device": "mobile",
    "os": "iOS 17.2",
    "browser": "Safari 17",
    "screen_size": "390x844",

    // Location
    "country": "US",
    "region": "California",
    "timezone": "America/Los_Angeles",

    // Campaign & Attribution
    "campaign_id": "email-001",
    "referrer": "email_campaign",
    "utm_source": "newsletter",
    "utm_campaign": "product_launch",

    // Customer Context
    "customer_tier": "enterprise",
    "industry": "healthcare",
    "team_size": "50-200",
    "plan": "pro",

    // Custom Fields
    "feature_flags": ["new_ui", "ai_features"],
    "session_duration": 1847,
    "pages_viewed": 12
  }
}
```

### Querying JSONB

PostgreSQL provides powerful operators for querying JSONB fields:

```sql
-- Extract a specific key
SELECT metadata->>'country' as country
FROM experience_data
WHERE source_type = 'survey';

-- Filter by nested value
SELECT * FROM experience_data
WHERE metadata->'device' @> '"mobile"';

-- Check if key exists
SELECT * FROM experience_data
WHERE metadata ? 'campaign_id';

-- Filter by numeric value
SELECT * FROM experience_data
WHERE (metadata->>'session_duration')::int > 300;

-- Array containment
SELECT * FROM experience_data
WHERE metadata->'feature_flags' @> '["ai_features"]';
```

:::tip Best Practice: Consistent Keys
Use snake_case naming and consistent key names across your metadata to make queries easier:

- ✅ `"customer_tier"` (consistent)
- ❌ `"customerTier"`, `"tier"`, `"customer-tier"` (inconsistent)
:::

[Learn more in PostgreSQL JSONB docs →](https://www.postgresql.org/docs/current/functions-json.html)

### Journey & Touchpoint Metadata (Recommended Convention)

For customer journey analysis, we recommend using these standardized metadata keys:

```json
{
  "metadata": {
    "journey_stage": "onboarding",
    "touchpoint": "checkout",
    "channel": "mobile_app",
    "interaction_type": "transaction"
  }
}
```

| Key | Recommended Values | Description |
| --- | ------------------ | ----------- |
| `journey_stage` | `awareness`, `consideration`, `purchase`, `onboarding`, `usage`, `renewal`, `advocacy` | Where the customer is in their lifecycle |
| `touchpoint` | Free-form (e.g., `checkout`, `support_ticket`, `feature_x`) | Specific interaction point |
| `channel` | `web`, `mobile_app`, `email`, `support`, `in_store`, `social` | Channel of interaction |
| `interaction_type` | `transaction`, `support`, `feedback_request`, `proactive` | Type of interaction that triggered feedback |

:::tip Journey Analysis Example
```sql
-- NPS by journey stage
SELECT 
  metadata->>'journey_stage' as stage,
  ROUND(AVG(value_number), 1) as avg_nps,
  COUNT(*) as responses
FROM experience_data
WHERE field_type = 'nps' AND metadata->>'journey_stage' IS NOT NULL
GROUP BY stage
ORDER BY avg_nps DESC;
```
:::

## Best Practices

### 1. Use Validated Field Types

Always use one of the 9 standardized field types. The API will reject invalid types:

✅ **Good:**

```json
{
  "field_type": "text", // Valid enum
  "field_type": "categorical", // Valid enum
  "field_type": "nps", // Valid enum
  "field_type": "ces" // Valid enum (Customer Effort Score)
}
```

❌ **Will Fail:**

```json
{
  "field_type": "openText", // Old name, now invalid
  "field_type": "multiple_choice", // Not a valid enum
  "field_type": "custom_scale" // Not a valid enum
}
```

### 2. Match Field Types to Value Columns

Only populate the appropriate `value_*` field based on `field_type`:

| Field Type                               | Correct Value Column | Example                  |
| ---------------------------------------- | -------------------- | ------------------------ |
| `text`, `categorical`                    | `value_text`         | `"Great product!"`       |
| `nps`, `csat`, `ces`, `rating`, `number` | `value_number`       | `9`                      |
| `boolean`                                | `value_boolean`      | `true`                   |
| `date`                                   | `value_date`         | `"2025-01-15T10:00:00Z"` |

✅ **Good:**

```json
{
  "field_type": "nps",
  "value_number": 9
}
```

❌ **Avoid:**

```json
{
  "field_type": "nps",
  "value_text": "9" // Wrong! Should be value_number
}
```

### 3. Use Consistent Field IDs

Keep field IDs stable across time for longitudinal analysis:

✅ **Good:**

```json
{
  "field_id": "nps_score", // Consistent
  "field_id": "improvement_feedback" // Consistent
}
```

❌ **Avoid:**

```json
{
  "field_id": "nps_q1_2025", // Don't include time periods
  "field_id": "question_1" // Too generic
}
```

### 4. Leverage AI Enrichment

For open-ended feedback, use `field_type: "text"` to get automatic sentiment, emotion, and topic
extraction:

```json
{
  "field_type": "text",
  "value_text": "The UI is great but pricing is confusing"
  // AI will automatically add:
  // "sentiment": "mixed"
  // "sentiment_score": 0.1
  // "emotion": "confused"
  // "topics": ["ui_design", "pricing"]
}
```

### 5. Multi-Select as Multiple Rows

For questions where users can select multiple options, create one row per selection:

```json
// Question: "Which features do you use?" (user selected 3 options)
// Create 3 rows with the same source_id, field_id, but different value_text

{"field_type": "categorical", "value_text": "Dashboards"},
{"field_type": "categorical", "value_text": "Reports"},
{"field_type": "categorical", "value_text": "Alerts"}
```

### 6. Anonymous User Identifiers

For privacy, use anonymous hashed IDs instead of PII:

✅ **Good:**

```json
{
  "user_identifier": "sha256:abc123def456"
}
```

❌ **Avoid:**

```json
{
  "user_identifier": "john@example.com" // Never store PII
}
```

### 7. Rich Metadata for Segmentation

Use `metadata` for contextual attributes that enable deeper analysis:

```json
{
  "metadata": {
    "customer_tier": "enterprise",
    "plan": "pro",
    "feature_flags": ["new_ui"],
    "account_age_days": 450
  }
}
```

## Next Steps

- [AI Enrichment](./ai-enrichment) - Automatic sentiment and topic extraction
- [Authentication](./authentication) - Secure your API
- [Webhooks](./webhooks) - React to new data
- [API Reference](../api-reference) - Explore all endpoints
