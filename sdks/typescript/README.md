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

Requests are retried and timed out by default: up to **2 retries**, **60 seconds per attempt**, exponential backoff with jitter, and a server's `Retry-After` honoured. A request is only sent again when that cannot apply it twice:

| Retried                                        | On                                                                                                  |
| ---------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Any request                                    | `429`; a `408` or `503` with `Retry-After`; a `409` whose problem `code` is `tenant_write_conflict` |
| `GET`, `HEAD`, `OPTIONS`, `PUT`, `DELETE` also | `500`, `502`, `504`, any other `408` or `503`, network errors, timed-out attempts                   |

`POST` and `PATCH` are not retried after a `500`, `502` or `504`, a `408` or `503` without `Retry-After`, a network error or a timeout, because the Hub may already have applied them. A proxy such as Envoy or Istio answers `503` when its connection to the Hub drops and `408` when its idle timeout fires, either of which can follow a committed write. A create the Hub already applied would come back as a `409` conflict, or — for a webhook — be created twice. Retry those deliberately if your call is safe to repeat, such as a semantic search. Other `4xx` responses are never retried, nor is a request you abort, nor a response whose `Retry-After` asks for more than 60 seconds: that response is returned for you to act on.

If you run the Hub behind your own proxy, don't have it turn upstream errors into `503` with `Retry-After` — that marks a request the Hub never saw, so `POST`s would be re-sent.

```ts
const client = createHubClient({
  apiKey,
  baseUrl,
  maxRetries: 4, // 0 turns retries off
  timeout: 10_000, // per attempt, in ms; 0 turns it off
});
```

The timeout covers each attempt until its body has been read, so a response that stalls mid-body can't hang a call; set `timeout: 0` for a long-lived stream. For `GET`, `HEAD`, `OPTIONS`, `PUT` and `DELETE`, a JSON body is read inside the attempt, so one that drops or stalls mid-response is retried like any other failure. A retried response's body is discarded unread. The timeout applies to each attempt separately; to bound a call as a whole, pass a `signal`, e.g. `AbortSignal.timeout(30_000)`.

Retries live in the client's `fetch`. A `fetch` passed per call, or set later with `client.setConfig({ fetch })`, replaces them unless it is a `createHubFetch()` itself.

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
