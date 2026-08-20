import assert from "node:assert/strict";
import http from "node:http";
import { after, before, describe, it } from "node:test";

// Deliberately imports the BUILT package, not src/: this exercises what actually
// ships, including the exports map, rather than the pre-bundle sources.
const { createHubClient, listFeedbackRecords, getTaxonomyRunTree } =
  await import("../dist/index.mjs");

/** Echoes the request line back so a test can assert the exact wire format. */
function startEchoServer() {
  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      res.setHeader("content-type", "application/json");
      res.end(
        JSON.stringify({
          url: req.url,
          authorization: req.headers.authorization ?? null,
        }),
      );
    });
    server.listen(0, "127.0.0.1", () =>
      resolve({ server, baseUrl: `http://127.0.0.1:${server.address().port}` }),
    );
  });
}

describe("@formbricks/hub wire format", () => {
  let server;
  let client;

  before(async () => {
    const started = await startEchoServer();
    server = started.server;
    client = createHubClient({ apiKey: "test-key", baseUrl: started.baseUrl });
  });

  after(() => server.close());

  // The regression this SDK exists to fix. Every repeatable filter in
  // openapi.yaml is `style: form, explode: true`, and the Hub does not split
  // commas: a comma-joined array is a 400 on the enum filters and, worse, a 200
  // with an empty page on the string filters — which a caller reads as "there is
  // no such feedback". The previous generator comma-joined them, and the
  // Formbricks app carried ~440 lines of client-side correction because of it.
  it("sends array filters as repeated parameters, not comma-joined", async () => {
    const { data } = await listFeedbackRecords({
      client,
      query: {
        tenant_id: "org-1",
        source_type: ["survey", "review"],
        sentiment: ["negative", "very_negative"],
      },
    });

    const params = new URL(data.url, "http://127.0.0.1").searchParams;
    assert.deepEqual(params.getAll("source_type"), ["survey", "review"]);
    assert.deepEqual(params.getAll("sentiment"), ["negative", "very_negative"]);

    // Belt and braces: assert the literal shape too, so a serializer that
    // produced `?source_type=survey%2Creview` cannot pass by round-tripping.
    assert.ok(
      data.url.includes("source_type=survey&source_type=review"),
      `expected repeated params, got ${data.url}`,
    );
    assert.ok(
      !data.url.includes("%2C"),
      `found a comma-joined array in ${data.url}`,
    );
  });

  it("authenticates with a bearer token", async () => {
    const { data } = await listFeedbackRecords({
      client,
      query: { tenant_id: "org-1" },
    });
    assert.equal(data.authorization, "Bearer test-key");
  });

  it("interpolates path parameters", async () => {
    const { data } = await getTaxonomyRunTree({
      client,
      path: { run_id: "run_123" },
      query: { tenant_id: "org-1" },
    });
    assert.ok(
      data.url.startsWith("/v1/taxonomy/runs/run_123/tree"),
      `unexpected path: ${data.url}`,
    );
  });
});
