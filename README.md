# Formbricks Hub

<div align="center">

**Unified Experience Data Platform**

Aggregate, enrich, and analyze customer feedback from surveys, reviews, and support tickets

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![CI](https://github.com/formbricks/hub/workflows/CI/badge.svg)](https://github.com/formbricks/hub/actions)
[![GitHub stars](https://img.shields.io/github/stars/formbricks/hub?style=social)](https://github.com/formbricks/hub/stargazers)

[Documentation](https://hub.formbricks.com/) · [Quick Start](#-quick-start) · [Community](#-community)

</div>

<!-- Updated: January 2025 -->

---

## 🎯 Overview

**Formbricks Hub** is an open-source unified experience data repository that solves the challenge of scattered customer feedback across multiple platforms. It provides a centralized system to collect, enrich with AI, and analyze feedback from surveys, product reviews, support tickets, and social media.

### The Problem

Customer feedback is scattered across multiple platforms:
- Survey tools (Formbricks, Typeform, Google Forms)
- Review sites (G2, Trustpilot, App Store)
- Support systems (Zendesk, Intercom)
- Social media (Twitter, Reddit)

Each platform has different data formats, making it impossible to get a unified view of customer sentiment and experience.

### The Solution

Hub provides:

✅ **Unified Data Model** - All feedback sources mapped to a single, analytics-optimized schema  
✅ **AI-Powered Enrichment** - Automatic sentiment analysis, emotion detection, and topic extraction  
✅ **BI-Ready Structure** - Optimized for tools like Apache Superset, Tableau, Power BI  
✅ **Real-time Webhooks** - React to new feedback immediately  
✅ **Cross-Source Analytics** - Analyze patterns across reviews, surveys, and support tickets

---

## ✨ Key Features

### 📊 Analytics-First Data Model

Hub uses a **narrow format** (one row per question-answer pair) optimized for SQL aggregations and BI tools. No complex JSON unnesting, no wide tables—just simple, queryable data:

```sql
-- Direct aggregation without JSON unnesting
SELECT sentiment, COUNT(*) as feedback_count
FROM experience_data 
WHERE field_type = 'text' AND collected_at > NOW() - INTERVAL '7 days'
GROUP BY sentiment;

-- Find all frustrated users mentioning "checkout"
SELECT value_text, sentiment_score, topics
FROM experience_data
WHERE emotion = 'frustration' 
  AND 'checkout' = ANY(topics);
```

### 🤖 AI-Powered Insights

Automatically enrich every text response with actionable insights using OpenAI:

- **Sentiment Analysis**: Positive, negative, neutral, or mixed with confidence scores (-1.0 to +1.0)
- **Emotion Detection**: Joy, frustration, anger, confusion, sadness automatically identified
- **Topic Extraction**: Automatically discover themes like pricing, UI, bugs, support—no manual tagging
- **Semantic Search**: Query feedback by meaning, not keywords: *"users frustrated with checkout"*
- **Vector Embeddings**: Powered by `pgvector` for fast similarity search at scale

**All AI processing happens asynchronously**—your API stays fast (~20-50ms response time) while enrichment runs in the background. Results appear within 5-15 seconds via webhook notifications.

**Cost-efficient**: ~$0.015 per 1,000 text responses with `gpt-4o-mini`.

### 🔌 Multi-Source Support

Centralize feedback from anywhere through our simple REST API:

- **Surveys**: Formbricks, Typeform, Google Forms, SurveyMonkey
- **Reviews**: G2, Trustpilot, Capterra (includes [G2 import script](scripts/data-imports/g2-reviews/))
- **App Stores**: Apple App Store, Google Play (coming soon)
- **Support**: Zendesk, Intercom, Help Scout (coming soon)
- **Social**: Twitter, Reddit (coming soon)
- **Custom**: Any source via REST API or Python scripts

### 📈 BI-Ready Analytics

Direct PostgreSQL access means instant integration with any SQL-compatible tool:

- **Real-time Dashboards**: Apache Superset, Metabase, Redash
- **Enterprise BI**: Power BI, Tableau, Looker, Qlik
- **Data Warehouses**: Snowflake, Redshift, BigQuery (export ready)
- **Custom SQL**: Write queries in your favorite tool

**Example queries:**
- Weekly NPS trends by source
- Sentiment distribution over time
- Most mentioned topics this month
- Emotion breakdowns by product area

### 🎯 Real-Time Webhooks

React to feedback the moment it arrives with reliable webhook delivery:

- **Event Types**: `experience.created`, `experience.enriched`, `experience.updated`, `experience.deleted`
- **Reliable Delivery**: Worker pool with 3 retries and exponential backoff
- **Fast**: 5-second timeout per webhook, never blocks API responses
- **Flexible**: Send to Slack, Zapier, n8n, or custom endpoints

**Use cases:**
- Alert Support when negative sentiment detected
- Trigger workflows when specific topics appear
- Sync to external systems in real-time
- Update dashboards immediately

---

## 🚀 Quick Start

Get up and running in under 5 minutes with Docker.

### Prerequisites

- **Docker** and **Docker Compose**: [Install Docker Desktop](https://www.docker.com/products/docker-desktop/)

### Installation

**1. Download the production Docker Compose file:**

```bash
mkdir formbricks-hub && cd formbricks-hub
curl -o docker-compose.yml https://raw.githubusercontent.com/formbricks/hub/main/docker-compose.prod.yml
```

**2. Configure your environment:**

Create a `.env` file with secure passwords:

```bash
# Required: Secure passwords
POSTGRES_PASSWORD=$(openssl rand -base64 32)
SERVICE_API_KEY=$(openssl rand -base64 32)

# Optional: OpenAI for AI enrichment and semantic search
SERVICE_OPENAI_API_KEY=sk-your-api-key-here

# Optional: Configuration
SERVICE_PORT=8080
SERVICE_LOG_LEVEL=info
```

💡 **Note**: Without `SERVICE_OPENAI_API_KEY`, Hub works perfectly but won't enrich text feedback with sentiment/topics or support semantic search.

**3. Start the services:**

```bash
docker compose -f docker-compose.prod.yml up -d
```

This starts:
- **Formbricks Hub API** (port 8080)
- **PostgreSQL with pgvector** (port 5432, automatically installs vector extension)

**4. Verify it's running:**

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

### Basic Usage

**Create an experience with text feedback (automatic AI enrichment):**

```bash
curl -X POST http://localhost:8080/v1/experiences \
  -H "X-API-Key: YOUR_SERVICE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "source_type": "survey",
    "source_id": "nps-2025-q1",
    "field_id": "feedback",
    "field_label": "What can we improve?",
    "field_type": "text",
    "value_text": "The checkout process is confusing and slow. Very frustrating!",
    "collected_at": "2025-01-15T10:30:00Z"
  }'
```

Within seconds, Hub automatically enriches the feedback with:
- **Sentiment**: `negative` (score: -0.8)
- **Emotion**: `frustration`
- **Topics**: `["checkout", "user_experience", "performance"]`

**Query feedback:**

```bash
curl "http://localhost:8080/v1/experiences?limit=10" \
  -H "X-API-Key: YOUR_SERVICE_API_KEY"
```

**Search feedback by meaning (semantic search):**

```bash
curl "http://localhost:8080/v1/experiences/search?q=frustrated%20checkout&limit=10" \
  -H "X-API-Key: YOUR_SERVICE_API_KEY"
```

📖 **For complete documentation, see the [Quick Start Guide](https://main.d3n9jg0z7xep8b.amplifyapp.com/quickstart)**

---

## 💻 Development Setup

For local development with hot-reload:

**1. Clone the repository:**

```bash
git clone https://github.com/formbricks/hub.git
cd hub
```

**2. Start development services:**

```bash
docker compose up -d  # PostgreSQL
```

**3. Run the Hub API:**

```bash
cd apps/hub
cp env.example .env
# Edit .env with your configuration
make dev
```

The API will be available at `http://localhost:8888`

---

## 🏗️ Architecture

```
┌─────────────────┐
│  External Data  │
│  (G2, Surveys)  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐      ┌──────────────┐
│   REST API      │◄─────┤  Webhooks    │
│   (Go/Huma)     │      │  (Outbound)  │
└────────┬────────┘      └──────────────┘
         │
         ▼
┌─────────────────┐      ┌──────────────┐
│   PostgreSQL    │◄─────┤ Job Queue    │
│   (UUIDv7)      │      │ (Async)      │
└────────┬────────┘      └──────┬───────┘
         │                      │
         ▼                      ▼
┌─────────────────┐      ┌──────────────┐
│   BI Tools      │      │  AI Worker   │
│   (Superset,    │      │  (OpenAI)    │
│    Power BI)    │      └──────────────┘
└─────────────────┘
```

**Key Components:**

- **Hub API** (Go): High-performance REST API with OpenAPI 3.1 documentation
- **PostgreSQL 18**: Primary database with pgvector extension for semantic search
- **Job Queue**: PostgreSQL-backed queue for reliable async processing
- **AI Workers**: Background workers for sentiment analysis, topic extraction, and embeddings
- **Webhook System**: Worker pool with retry logic for reliable event delivery
- **BI Tools**: Direct SQL access for Apache Superset, Power BI, Tableau, Looker
- **Import Scripts** (Python): Data connectors for external sources (G2, Formbricks)

---

## 📁 Repository Structure

```
formbricks-hub/
├── apps/
│   ├── hub/          # Go API service (REST + workers)
│   └── docs/           # Docusaurus documentation site
├── scripts/
│   └── data-imports/   # Python scripts for data sources
│       ├── g2-reviews/
│       └── formbricks-surveys/
├── packages/           # Shared configs (ESLint, TypeScript)
├── docker-compose.yml  # Local development stack
└── turbo.json         # Turborepo configuration
```

---

## 📚 Documentation

Visit our [documentation site](https://main.d3n9jg0z7xep8b.amplifyapp.com) for complete guides:

- **[Quick Start](https://main.d3n9jg0z7xep8b.amplifyapp.com/quickstart)** - Get up and running in 5 minutes
- **[Data Model](https://main.d3n9jg0z7xep8b.amplifyapp.com/core-concepts/data-model)** - Understanding the schema
- **[AI Enrichment](https://main.d3n9jg0z7xep8b.amplifyapp.com/core-concepts/ai-enrichment)** - Automatic sentiment and topic extraction
- **[Semantic Search](https://main.d3n9jg0z7xep8b.amplifyapp.com/core-concepts/semantic-search)** - Query feedback by meaning
- **[Webhooks](https://main.d3n9jg0z7xep8b.amplifyapp.com/core-concepts/webhooks)** - React to feedback in real-time
- **[API Reference](https://main.d3n9jg0z7xep8b.amplifyapp.com/api-reference)** - Complete REST API documentation
- **[Environment Variables](https://main.d3n9jg0z7xep8b.amplifyapp.com/reference/environment-variables)** - Configuration reference

---

## 🤝 Community

We'd love your feedback and contributions!

- **GitHub Discussions**: Ask questions and share ideas
- **Issues**: Report bugs or request features
- **Contributing**: See [CONTRIBUTING.md](CONTRIBUTING.md)
- **Security**: Report vulnerabilities to security@formbricks.com

### Ways to Contribute

- 🐛 Report bugs
- 💡 Suggest features
- 📝 Improve documentation
- 🔌 Build data source connectors
- ⭐ Star the repository

---

## 📊 Example Use Cases

**Product Teams:**
- Track NPS trends over time across all feedback sources
- Use semantic search to find feature requests ("users want dark mode")
- Automatically identify top pain points from open-ended feedback
- Correlate sentiment changes with product releases

**Support Teams:**
- Get alerted when "angry" or "frustrated" feedback arrives (webhooks)
- Analyze sentiment trends in support tickets by topic
- Identify common issues before they become widespread
- Measure customer satisfaction (CSAT) across all channels

**Marketing Teams:**
- Monitor brand sentiment across review sites in real-time
- Track campaign feedback with automatic topic extraction
- Compare sentiment across channels (email vs social vs in-app)
- Discover what customers love most (joy + positive sentiment)

**Data Teams:**
- Build unified feedback dashboards in your favorite BI tool
- Export enriched data to Snowflake/Redshift for deeper analysis
- Train custom ML models on sentiment-labeled data
- Query feedback using natural language (semantic search)

---

## 🔐 Security

Formbricks Hub is designed with security best practices:

- **API Key Authentication**: Timing-attack resistant constant-time comparison
- **Rate Limiting**: Per-IP and global rate limits to prevent abuse
- **Request Size Limits**: 10MB max body size to prevent memory exhaustion
- **Sanitized Error Messages**: Generic errors returned to clients, detailed logs internally
- **No PII Storage**: Hub doesn't require personally identifiable information
- **Dependency Scanning**: Automated security updates via Dependabot

**Report security vulnerabilities to:** security@formbricks.com

See [SECURITY.md](SECURITY.md) for full security details.

---

## 📄 License

Formbricks Hub is open-source software licensed under the **Apache License 2.0**.

See [LICENSE](LICENSE) for the full license text.

---

## 🙏 Acknowledgments

Built with ❤️ by the [Formbricks team](https://formbricks.com)

Powered by:
- [Go](https://go.dev/) - Performance and concurrency
- [Huma](https://huma.rocks/) - OpenAPI-first REST framework
- [Ent](https://entgo.io/) - Type-safe ORM with code generation
- [PostgreSQL](https://www.postgresql.org/) - Robust database
- [OpenAI](https://openai.com/) - AI-powered enrichment
- [Apache Superset](https://superset.apache.org/) - Open-source BI

---

<div align="center">

**[⭐ Star us on GitHub](https://github.com/formbricks/hub)** if you find this project useful!

</div>
