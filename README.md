# Formbricks Hub

**Agentic-first Experience Management Datastore**

Formbricks Hub is the open, structured data layer for Experience Management (XM): a standalone, open-source datastore to unify feedback records from across different sources in one place so humans, BI tools, and AI agents can act on them.

It is the foundation for agentic XM workflows and is also used by the [Formbricks XM Suite](https://github.com/formbricks/formbricks).

## Why Formbricks Hub

Experience Management is broken in most organizations.

Teams collect feedback, build dashboards, and generate reports, but action remains manual, slow, and fragmented. Data is scattered across tools, formats, and teams, which makes it hard to understand what is happening and even harder to respond in time.

Formbricks Hub is built to fix that by giving you a single, open experience data layer designed for continuous analysis and action.

Its data model is intentionally analytics-friendly: feedback is stored in a structured format that makes it easy to query with SQL, aggregate across sources, and use in reporting tools without heavy transformations.

## What This Repository Is (and Is Not)

This repository is the right place for you if you want:

- An open-source, self-hostable XM datastore for feedback records
- A central system to unify experience signals from many sources
- A backend service your own apps, pipelines, BI tools, and agents can build on
- A foundation for AI analysis and semantic workflows (including embeddings-powered use cases)
- A SQL-friendly feedback data model for reporting and analytics

This repository is **not** the full Formbricks survey/XM product UI.

If you are looking for the complete Formbricks application (surveys, UI, broader platform capabilities), use the main repository:

- [Formbricks XM Suite (`formbricks/formbricks`)](https://github.com/formbricks/formbricks)

## Built for the AI Age

Hub is being prepared as the core datastore for agentic experience workflows.

Formbrocks Hub enables the next generation of AI-powered analysis and semantic search workflows on top of experience data.

This makes Hub a strong fit if you want to build:

- AI copilots for CX / PX / UX teams
- Automated feedback triage and routing
- Semantic search across feedback records
- Root-cause investigation agents
- Experience monitoring and alerting automations

Hub is also designed to work well with standard analytics workflows:

- Structured records that are straightforward to query with SQL
- A simple feedback-centric schema that reduces BI modeling friction
- Direct PostgreSQL connectivity for dashboards and reporting
- A strong foundation for both classical analytics and agentic AI workflows

Learn more:

- [Data Model (Core Concept)](https://hub.formbricks.com/core-concepts/data-model/)

## Ecosystem

Formbricks Hub is an independent open-source project with a growing developer ecosystem:

- Documentation: [hub.formbricks.com](https://hub.formbricks.com/)
- Data Model: [hub.formbricks.com/core-concepts/data-model](https://hub.formbricks.com/core-concepts/data-model/)
- Power BI Guide: [Connect Hub to Microsoft Power BI](https://hub.formbricks.com/guides/hub-powerbi/)
- Superset Guide: [Connect Hub to Apache Superset](https://hub.formbricks.com/guides/hub-superset/)
- TypeScript SDK: [`@formbricks/hub`](https://www.npmjs.com/package/@formbricks/hub)
- MCP Server: [`@formbricks/hub-mcp`](https://www.npmjs.com/package/@formbricks/hub-mcp)
- Formbricks XM Suite (uses Hub): [`formbricks/formbricks`](https://github.com/formbricks/formbricks)

## Getting Started

If you want to evaluate Hub quickly, start with the docs:

- [Introduction](https://hub.formbricks.com/)
- [Quick Start Guide](https://hub.formbricks.com/quickstart/)
- [Data Model](https://hub.formbricks.com/core-concepts/data-model/)
- [API Reference](https://hub.formbricks.com/api)
- [Connect Hub to Microsoft Power BI](https://hub.formbricks.com/guides/hub-powerbi/)
- [Connect Hub to Apache Superset](https://hub.formbricks.com/guides/hub-superset/)

## Who Hub Is For

Formbricks Hub is a good fit for:

- Product and CX teams that want a unified feedback datastore
- Engineering teams building internal XM tooling
- Data teams that need structured, analytics-ready experience records
- BI / analytics teams that want direct reporting on feedback data (e.g. Power BI, Superset)
- AI/agent builders who need a reliable experience data backend
- Organizations that prefer open-source and self-hostable infrastructure

## Contributing

We welcome issues, ideas, and contributions as Hub evolves toward agentic XM infrastructure.

- [Report an issue](https://github.com/formbricks/hub/issues)
- [Discussions](https://github.com/formbricks/hub/discussions)

## License

Apache 2.0. See [`LICENSE`](./LICENSE).
