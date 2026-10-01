# @formbricks/hub

The official TypeScript client for the [Formbricks Hub](https://hub.formbricks.com) API — feedback-record storage, search, enrichment and webhooks.

```bash
npm install @formbricks/hub
```

On Node.js, 22 or later. The client is built on the standard `fetch`, so it also runs in browsers, Deno and Bun.

## Usage

```ts
import { createHubClient, listFeedbackRecords } from "@formbricks/hub";

const client = createHubClient({
  apiKey: process.env.HUB_API_KEY!,
  baseUrl: "https://app.formbricks.com", // or your self-hosted Hub
});

const { data } = await listFeedbackRecords({
  client,
  query: {
    tenant_id: "org-123",
    source_type: ["survey", "review"],
    sentiment: ["negative", "very_negative"],
    limit: 50,
  },
});
```

`baseUrl` is required rather than defaulted: the Hub is self-hostable, so there is no single correct origin.

Every operation is a standalone function, so bundlers can tree-shake what you don't call. If you need interceptors or extra headers, use the generated client directly — and pass it `createHubFetch()` to keep the retries described below:

```ts
import { createClient, createConfig, createHubFetch } from "@formbricks/hub";

const client = createClient(
  createConfig({ baseUrl, auth: () => apiKey, fetch: createHubFetch() }),
);
```

Calls return `{ data, error, response }` rather than throwing on an error status; pass `throwOnError: true` to throw the parsed error body instead. Network failures and timeouts come back in `error` the same way.

## Retries and timeouts

Requests are retried and timed out by default: up to **2 retries**, **60 seconds per attempt**, exponential backoff with jitter, and a server's `Retry-After` honoured. A request is only sent again when that cannot write anything twice:

| Retried                                        | On                                                                                                    |
| ---------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Any request                                    | `408`, `429`, a `503` with `Retry-After`, and a `409` whose problem `code` is `tenant_write_conflict` |
| `GET`, `HEAD`, `OPTIONS`, `PUT`, `DELETE` also | `500`, `502`, `504`, any other `503`, network errors, timed-out attempts                              |

`POST` and `PATCH` are not retried after a `500`, `502` or `504`, a `503` without `Retry-After`, a network error or a timeout, because the Hub may already have applied them — a proxy such as Envoy or Istio answers `503` when the connection to the Hub drops, which can be after the write committed. Retry those deliberately if your call is safe to repeat. Other `4xx` responses are never retried, nor is a request you abort.

```ts
const client = createHubClient({
  apiKey,
  baseUrl,
  maxRetries: 4, // 0 turns retries off
  timeout: 10_000, // per attempt, in ms; 0 turns it off
});
```

The timeout applies to each attempt separately; to bound a call as a whole, pass a `signal`, e.g. `AbortSignal.timeout(30_000)`.

## Runtime validation

Zod schemas for every request and response shape — carrying the spec's own `minLength`, `maxLength`, `pattern` and enum constraints — are available from a separate entry point, so importing the client itself pulls in no dependencies:

```ts
import { zFeedbackRecordData } from "@formbricks/hub/schemas";
```

`zod` is an optional peer dependency; install it only if you use this entry point.

## Repeated query parameters

Array filters are sent as repeated parameters (`?source_type=survey&source_type=review`), which is what the Hub expects — it does not split comma-separated values. Pass arrays and the client does the right thing.

## How this package is built

It is generated from [`openapi.yaml`](https://github.com/formbricks/hub/blob/main/openapi.yaml) in the Hub repository by [`@hey-api/openapi-ts`](https://heyapi.dev/), and published from that repository on release with [npm provenance](https://docs.npmjs.com/generating-provenance-statements). The generated source is not committed — this tarball ships it under `src/`, and the provenance attestation names the exact commit it came from, so you can regenerate and compare.

To report a problem or request a change to the API surface, open an issue in [formbricks/hub](https://github.com/formbricks/hub/issues).

## License

Apache-2.0
