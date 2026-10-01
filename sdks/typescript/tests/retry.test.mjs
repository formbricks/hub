// The retry policy decides whether a write can happen twice, so it is tested
// the way it runs: the BUILT package, its generated client building the
// Request, and a real HTTP server answering. A test that called the wrapper
// with a hand-made Request would miss the one thing most likely to break — the
// generated client hands fetch a Request whose body can be read only once.
//
// Retryable responses carry `Retry-After: 0` so these run without the default
// backoff; the cases that cannot (network errors, timeouts) use one retry.

import assert from "node:assert/strict";
import http from "node:http";
import { after, before, beforeEach, describe, it } from "node:test";

const {
  createHubClient,
  createHubFetch,
  createFeedbackRecord,
  deleteFeedbackRecord,
  getFeedbackRecord,
  listFeedbackRecords,
} = await import("../dist/index.mjs");

const NOW = { "retry-after": "0" };
const problem = (status, code, headers = {}) => ({
  status,
  headers: { "content-type": "application/problem+json", ...headers },
  body: { status, code, title: "problem" },
});
const ok = (body = { ok: true }) => ({ status: 200, body });
const HANG = Symbol("hang");

// Each test queues the responses it wants, in order; requests past the end of
// the queue get a 200. Every request is recorded with its body, so a test can
// assert both how many attempts were made and what each one sent.
let script = [];
let requests = [];

const server = http.createServer((req, res) => {
  const chunks = [];
  req.on("data", (chunk) => chunks.push(chunk));
  req.on("end", () => {
    requests.push({
      method: req.method,
      url: req.url,
      body: Buffer.concat(chunks).toString(),
    });
    const next = script.shift() ?? ok();
    if (next === HANG) return; // never answers; closed in `after`
    res.writeHead(next.status, {
      "content-type": "application/json",
      ...next.headers,
    });
    res.end(JSON.stringify(next.body ?? {}));
  });
});

let baseUrl;
before(
  () =>
    new Promise((resolve) =>
      server.listen(0, "127.0.0.1", () => {
        baseUrl = `http://127.0.0.1:${server.address().port}`;
        resolve();
      }),
    ),
);
after(() => {
  server.closeAllConnections();
  server.close();
});
beforeEach(() => {
  script = [];
  requests = [];
});

const hub = (options = {}) =>
  createHubClient({ apiKey: "k", baseUrl, ...options });
const record = { tenant_id: "org-1", field_id: "f", field_type: "text" };

describe("retries: statuses the server did not act on, any method", () => {
  it(
    "retries a GET on 503 and returns the eventual success",
    { timeout: 10_000 },
    async () => {
      script = [problem(503, "unavailable", NOW), ok({ id: "r1" })];
      const { data } = await getFeedbackRecord({
        client: hub(),
        path: { id: "r1" },
      });
      assert.equal(requests.length, 2);
      assert.deepEqual(data, { id: "r1" });
    },
  );

  it(
    "retries a POST on 429, re-sending the same body",
    { timeout: 10_000 },
    async () => {
      // The body-replay case: without a clone per attempt, the retry would go
      // out empty, because the first attempt consumed the Request's body.
      script = [problem(429, "rate_limited", NOW), ok()];
      await createFeedbackRecord({ client: hub(), body: record });
      assert.equal(requests.length, 2);
      assert.equal(requests[0].method, "POST");
      assert.ok(requests[0].body.length > 0, "the first attempt sent a body");
      assert.equal(requests[1].body, requests[0].body);
    },
  );

  it(
    "retries a POST on a 409 the API documents as retryable",
    { timeout: 10_000 },
    async () => {
      script = [problem(409, "tenant_write_conflict", NOW), ok()];
      await createFeedbackRecord({ client: hub(), body: record });
      assert.equal(requests.length, 2);
    },
  );
});

describe("no retries where a write may already have happened", () => {
  it(
    "does not retry a POST on 500 — it may have been applied",
    { timeout: 10_000 },
    async () => {
      script = [problem(500, "internal", NOW)];
      const { error, response } = await createFeedbackRecord({
        client: hub(),
        body: record,
      });
      assert.equal(requests.length, 1);
      assert.equal(response.status, 500);
      assert.equal(error.code, "internal");
    },
  );

  it(
    "does not retry a POST on any other 409, and leaves the body readable",
    { timeout: 10_000 },
    async () => {
      // A duplicate-row conflict is final. The policy reads the problem code from
      // a clone, so the caller must still get the original body intact.
      script = [problem(409, "duplicate", NOW)];
      const { error } = await createFeedbackRecord({
        client: hub(),
        body: record,
      });
      assert.equal(requests.length, 1);
      assert.equal(error.code, "duplicate");
    },
  );

  it(
    "does not retry other 4xx, even for a GET",
    { timeout: 10_000 },
    async () => {
      script = [problem(404, "not_found", NOW)];
      await getFeedbackRecord({ client: hub(), path: { id: "x" } });
      assert.equal(requests.length, 1);
    },
  );

  it(
    "does retry the same 500 for an idempotent method",
    { timeout: 10_000 },
    async () => {
      script = [problem(500, "internal", NOW), ok()];
      await deleteFeedbackRecord({ client: hub(), path: { id: "r1" } });
      assert.equal(requests.length, 2);
      assert.equal(requests[1].method, "DELETE");
    },
  );
});

describe("limits", () => {
  it(
    "gives up after maxRetries and returns the last response",
    { timeout: 10_000 },
    async () => {
      script = [503, 503, 503, 503].map((s) => problem(s, "unavailable", NOW));
      const { response } = await listFeedbackRecords({
        client: hub(),
        query: { tenant_id: "org-1" },
      });
      assert.equal(requests.length, 3, "one attempt plus two retries");
      assert.equal(response.status, 503);
    },
  );

  it("maxRetries: 0 turns retries off", { timeout: 10_000 }, async () => {
    script = [problem(503, "unavailable", NOW), ok()];
    await listFeedbackRecords({
      client: hub({ maxRetries: 0 }),
      query: { tenant_id: "org-1" },
    });
    assert.equal(requests.length, 1);
  });

  it(
    "does not wait out a Retry-After longer than its cap",
    { timeout: 10_000 },
    async () => {
      // The server asks for two minutes; the retry happens on the default
      // backoff instead, well inside the test's own timeout.
      script = [problem(503, "unavailable", { "retry-after": "120" }), ok()];
      const started = Date.now();
      await listFeedbackRecords({
        client: hub({ maxRetries: 1 }),
        query: { tenant_id: "org-1" },
      });
      assert.equal(requests.length, 2);
      assert.ok(
        Date.now() - started < 5_000,
        "waited out the 120s Retry-After",
      );
    },
  );
});

describe("timeouts", () => {
  it(
    "times out a hung attempt instead of waiting forever",
    { timeout: 10_000 },
    async () => {
      script = [HANG];
      const started = Date.now();
      await assert.rejects(
        listFeedbackRecords({
          client: hub({ timeout: 200, maxRetries: 0 }),
          query: { tenant_id: "org-1" },
          throwOnError: true,
        }),
        (error) => error.name === "TimeoutError",
      );
      assert.ok(Date.now() - started < 2_000);
    },
  );

  it("retries a timed-out GET", { timeout: 10_000 }, async () => {
    script = [HANG, ok({ second: true })];
    const { data } = await listFeedbackRecords({
      client: hub({ timeout: 200, maxRetries: 1 }),
      query: { tenant_id: "org-1" },
    });
    assert.equal(requests.length, 2);
    assert.deepEqual(data, { second: true });
  });

  it(
    "does not retry a timed-out POST — the server may still apply it",
    { timeout: 10_000 },
    async () => {
      script = [HANG, ok()];
      await assert.rejects(
        createFeedbackRecord({
          client: hub({ timeout: 200, maxRetries: 1 }),
          body: record,
          throwOnError: true,
        }),
        (error) => error.name === "TimeoutError",
      );
      assert.equal(requests.length, 1);
    },
  );

  it(
    "never retries a request the caller aborted",
    { timeout: 10_000 },
    async () => {
      script = [HANG, ok()];
      const controller = new AbortController();
      let abortedAt;
      setTimeout(() => {
        abortedAt = Date.now();
        controller.abort();
      }, 100);
      await assert.rejects(
        listFeedbackRecords({
          client: hub({ timeout: 5_000 }),
          query: { tenant_id: "org-1" },
          signal: controller.signal,
          throwOnError: true,
        }),
        (error) => error.name === "AbortError",
      );
      assert.equal(requests.length, 1);
      // A retried abort would also send nothing — an aborted fetch rejects before
      // it connects — but it would sit through the backoff first. Promptness is
      // what tells "gave up" from "kept trying".
      assert.ok(Date.now() - abortedAt < 250, "kept going after the abort");
    },
  );
});

describe("wiring", () => {
  it(
    "gives the generated default client the same retries",
    { timeout: 10_000 },
    async () => {
      // The `client` an operation uses when called without one comes from the
      // generator's runtimeConfigPath hook, not from createHubClient.
      script = [problem(503, "unavailable", NOW), ok()];
      await listFeedbackRecords({ baseUrl, query: { tenant_id: "org-1" } });
      assert.equal(requests.length, 2);
    },
  );

  it("rejects options that cannot mean anything", () => {
    assert.throws(() => createHubFetch({ maxRetries: -1 }), RangeError);
    assert.throws(() => createHubFetch({ maxRetries: 1.5 }), RangeError);
    assert.throws(() => createHubFetch({ timeout: -1 }), RangeError);
    assert.throws(() => createHubFetch({ timeout: Infinity }), RangeError);
  });
});
