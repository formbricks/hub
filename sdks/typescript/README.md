# @formbricks/hub

The official TypeScript client for the [Formbricks Hub](https://hub.formbricks.com) API — feedback-record storage, search, enrichment and webhooks.

```bash
npm install @formbricks/hub
```

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

Every operation is a standalone function, so bundlers can tree-shake what you don't call. If you need a custom `fetch`, interceptors or extra headers, use the generated client directly:

```ts
import { createClient, createConfig } from "@formbricks/hub";
```

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
