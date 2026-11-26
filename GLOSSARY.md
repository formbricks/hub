# Glossary

Domain terms the code and architecture reviews use. Only terms specific to the Hub belong here: no general
programming concepts, and no implementation details.

## Feedback

**Feedback record** — One answer to one field, stored with its source, its field and one typed value (text, number, boolean or date). Records that came in together share a submission. _Avoid_: response, answer, entry.

**Submission** — The group of feedback records captured together, identified by the mandatory `submission_id`. _Avoid_: response.

**Source** — The system a feedback record came from, named by `source_type` and optionally `source_id` and `source_name`. A record with no `source_id` falls in the "no source" bucket.

**Field** — The question or input a feedback record answers, identified by `field_id` within its source and typed by `field_type`. Fields may share a field group.

## Tenancy

**Tenant** — The isolation boundary for every tenant-owned resource, identified by `tenant_id`. Tenant-owned data is never read, enqueued, searched, embedded or deleted across tenants. The one exception is the documented GDPR erasure flow, which deletes one data subject's records in every tenant. _Avoid_: account, organization.

**Tenant settings** — A tenant's persisted configuration, today the enrichment switches. Each setting falls back to a documented default when unset.

**Purge** — Deleting all of a tenant's feedback records. No other write to the tenant's data runs while a purge does.

## Enrichment

**Enrichment** — A server-generated, read-only output derived from a feedback record's text: translation, sentiment, emotions or embedding. It is empty until computed, and cleared and recomputed when the text changes.

**Classify enrichment** — An enrichment that turns text into a structured result through an LLM: translation, sentiment and emotions. Embedding is not one.

**Embedding** — The vector computed from a feedback record's text, used for semantic search and taxonomy generation.

**Backfill** — A one-off command that enqueues an enrichment for the existing backlog of feedback records.

## Taxonomy

**Taxonomy run** — One generation of a topic tree over a scope of feedback records: pending, running, then succeeded, failed or canceled. A succeeded run becomes the active one for its scope.

**Taxonomy scope** — The feedback a taxonomy run reads: one field of one source (`field`), or every feedback record of the tenant (`directory`).

**Taxonomy node** — One node of a run's tree: root, branch or leaf. Nodes can be renamed or removed after generation.

**Taxonomy cluster** — A group of similar feedback records a taxonomy run produced from their embeddings.

## Delivery

**Webhook** — A tenant-owned subscription that receives the Hub's events for its tenant. A webhook always has a tenant; an event without one matches none.

**Event** — A change the Hub announces to webhooks, such as a feedback record created, updated or deleted.
