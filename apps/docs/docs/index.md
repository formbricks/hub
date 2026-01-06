---
sidebar_position: 1
slug: /
---

# Formbricks Hub

Standalone, fully open-source Experience Data Hub built for human and AI analytics.

## What is it?

Hub is an open-source, self-hostable microservice for **collecting, serving and enriching** experience data: Survey responses, NPS scores, product reviews, support feedback, and more. Built entirely in Go for performance and simplicity.

## Why should you use Hub?

- 🚀 **High Performance**: Go-powered microservice handles high write volume
- 🤖 **AI-Native**: Automatic sentiment analysis, topic extraction, semantic search, and more
- 📊 **Analytics-Ready**: Plug-and-play integrations for Apache Superset, Power BI, Tableau, Looker, Snowflake, etc.
- 🔐 **Self-Hostable**: Run in a single Docker container with PostgreSQL
- 🛠️ **Developer-Friendly**: OpenAPI spec, webhooks, clean REST API
- ⚡ **Event-driven**: Everything that happens in the Hub triggers an event. Send anywhere with webhooks!
- 💸 **Fully open-source**: The full Hub is and will be available open-source

## Why are we building the Hub? 🤔
We, like you, are passionate about living enjoyable lives. And we, like you, live in organized, highly structured societies where organizations shape the experiences we have day-to-day: Taking public transport, going to the gym, seeing the doctor, purchasing something, etc. Behind each of these experiences sits an organization that tries to provide you with the best possible experience - and more often than not, simply fails to do so.

We don't think they fail because they are don't try hard enough or are incapable, quite the opposite: Over the years, we've seen from the inside that many want to do better, try to make your life better, but they are stuck with closed, scattered systems and a lot of "we have always done it like that" and "we don't have money for that" from their bosses.

The Hub is a cornerstone of our work at Formbricks to help alleviate that burden and, ultimately, make your life better 😇

We are engineers, not consultants, and we bring open-source tooling to the table. We work closely with organizations and consultants to get them implemented and improve experiences. The past few years have been fun and challenging and we now understand one thing: 10 organizations do Experience Management in 10 different ways. It doesn't really work to force-feed them this new way of gathering data, this new framework to see their data in, this new approach to convince your peers to now actually make decisions based on experience data. It's simply to costly to setup and to complicated to weave into existing processes and workflows.

This is where the Hub comes in: It's free, it's simple, and, most importantly, it's built for the people who have to carry a lot of the burden around wrestling with experience data: software engineers.

## Use Cases

### Centralize Feedback Data

Collect experience data from multiple sources into one unified data hub:
- Survey responses from your app or website
- App Hub and Google Play reviews
- Trustpilot and other review platforms
- Support ticket feedback (Intercom, Zendesk)
- Social media sentiment

### Power Analytics Dashboards

Hub's schema is optimized for direct SQL queries and BI tool integration:
- Connect Apache Superset for real-time dashboards
- Build Power BI or Tableau reports without complex ETL
- Export to Snowflake or Redshift for data warehousing
- Perform time-series analysis on feedback trends

### AI-Powered Insights

Hub automatically enriches text feedback with actionable insights:
- **Sentiment analysis**: Positive, negative, or neutral classification
- **Emotion detection**: Joy, anger, sadness, and more
- **Topic extraction**: Automatically identify themes in feedback
- **Semantic search**: Find similar responses using natural language queries

[Learn more about AI enrichment →](./core-concepts/ai-enrichment)

### Listen to all Hub Events via Webhooks

Each Hub event is available as a webhook. Use webhooks to trigger actions based on enriched feedback:

- Send Slack notifications for low NPS scores or negative sentiment
- Route negative reviews to your support team automatically
- Update CRM records with AI-extracted insights
- Build custom dashboards with semantic search capabilities

Workflows are not a native part of the Hub, but you can connect it with solution like n8n, Zapier and soon Formbricks Workflows.

### Future: Connector Ecosystem

An open connector ecosystem is planned to simplify data integration. [Learn more →](./core-concepts/connectors)


## Quick Links

<div className="row">
  <div className="col col--6">
    <div className="card margin-bottom--lg">
      <div className="card__header">
        <h3>🚀 Get Started</h3>
      </div>
      <div className="card__body">
        <p>Set up Hub locally in 5 minutes</p>
      </div>
      <div className="card__footer">
        <a href="./quickstart" className="button button--primary button--block">
          Quick Start Guide
        </a>
      </div>
    </div>
  </div>
  <div className="col col--6">
    <div className="card margin-bottom--lg">
      <div className="card__header">
        <h3>📚 Learn the Basics</h3>
      </div>
      <div className="card__body">
        <p>Understand Hub's data model and concepts</p>
      </div>
      <div className="card__footer">
        <a href="./core-concepts/data-model" className="button button--secondary button--block">
          Core Concepts
        </a>
      </div>
    </div>
  </div>
</div>

<div className="row">
  <div className="col col--6">
    <div className="card margin-bottom--lg">
      <div className="card__header">
        <h3>🔧 API Reference</h3>
      </div>
      <div className="card__body">
        <p>Interactive API documentation and testing</p>
      </div>
      <div className="card__footer">
        <a href="./api-reference" className="button button--secondary button--block">
          Explore API
        </a>
      </div>
    </div>
  </div>
  <div className="col col--6">
    <div className="card margin-bottom--lg">
      <div className="card__header">
        <h3>⚙️ Configuration</h3>
      </div>
      <div className="card__body">
        <p>Environment variables and settings reference</p>
      </div>
      <div className="card__footer">
        <a href="./reference/environment-variables" className="button button--secondary button--block">
          Configuration Guide
        </a>
      </div>
    </div>
  </div>
</div>

## Community & Support

- **GitHub**: [formbricks/hub](https://github.com/formbricks/hub)
- **Discussions**: [Ask questions and share ideas](https://github.com/formbricks/hub/discussions)
- **Issues**: [Report bugs or request features](https://github.com/formbricks/hub/issues)
- **Documentation**: You're reading it! 📖
