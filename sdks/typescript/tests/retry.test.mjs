// The retry policy decides whether a write can happen twice, so it is tested
// the way it runs: the BUILT package, its generated client building the
// Request, and a real HTTP server answering. A test that called the wrapper
// with a hand-made Request would miss the one thing most likely to break — the
// generated client hands fetch a Request whose body can be read only once.
//
// Retryable responses carry `Retry-After: 0` so these run without the default
// backoff; the cases that cannot (network errors, timeouts, bare 408/503s) use
// one retry.

import assert from "node:assert/strict";
import http from "node:http";
import { after, before, beforeEach, describe, it } from "node:test";
import v8 from "node:v8";
import vm from "node:vm";

// Garbage collection on demand, for the cases where it decides the outcome.
v8.setFlagsFromString("--expose-gc");
const gc = vm.runInNewContext("gc");

const {
  createHubClient,
  createHubFetch,
  createFeedbackRecord,
  deleteFeedbackRecord,
  getFeedbackRecord,
  listFeedbackRecords,
  updateFeedbackRecord,
} = await import("../dist/index.mjs");

const NOW = { "retry-after": "0" };
const problem = (status, code, headers = {}) => ({
  status,
  headers: { "content-type": "application/problem+json", ...headers },
  body: { status, code, title: "problem" },
});
const ok = (body = { ok: true }) => ({ status: 200, body });
const HANG = Symbol("hang");
// Sends a 503's headers and half its body, then drops the connection.
const BROKEN_503 = Symbol("broken 503");
// Sends a 200's headers and half its JSON body, then drops the connection.
const BROKEN_200 = Symbol("broken 200");
// Drops the connection without answering at all.
const RESET = Symbol("reset");
// Sends a 200's headers at once and its JSON body a second later.
const SLOW_JSON = Symbol("slow json");
// Streams a plain-text body for 600ms, longer than the tests' short timeouts.
const SLOW_STREAM = Symbol("slow stream");

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
    if (next === RESET) return res.destroy();
    if (next === BROKEN_200) {
      res.writeHead(200, { "content-type": "application/json" });
      res.write('{"partial":');
      setTimeout(() => res.destroy(), 20);
      return;
    }
    if (next === SLOW_JSON) {
      res.writeHead(200, { "content-type": "application/json" });
      res.write("{");
      const timer = setTimeout(() => res.end('"slow":true}'), 1_000);
      res.on("close", () => clearTimeout(timer));
      return;
    }
    if (next === SLOW_STREAM) {
      res.writeHead(200, { "content-type": "text/plain" });
      let sent = 0;
      const timer = setInterval(() => {
        res.write(`chunk ${sent}\n`);
        if (++sent === 6) {
          clearInterval(timer);
          res.end();
        }
      }, 100);
      res.on("close", () => clearInterval(timer));
      return;
    }
    if (next === BROKEN_503) {
      res.writeHead(503, {
        "content-type": "application/json",
        "content-length": "1000",
      });
      res.write('{"partial":');
      setTimeout(() => res.destroy(), 20);
      return;
    }
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
  it(
    "retries a POST on a 503 that carries Retry-After",
    { timeout: 10_000 },
    async () => {
      // Retry-After marks the 503 as a deliberate refusal, not a proxy's
      // report that the upstream connection reset mid-request.
      script = [problem(503, "unavailable", NOW), ok()];
      await createFeedbackRecord({ client: hub(), body: record });
      assert.equal(requests.length, 2);
      assert.equal(requests[1].body, requests[0].body);
    },
  );
});

describe("retries: refusals that carry Retry-After, any method", () => {
  it(
    "retries a POST on a 408 with Retry-After",
    { timeout: 10_000 },
    async () => {
      script = [problem(408, "timeout", NOW), ok()];
      await createFeedbackRecord({ client: hub(), body: record });
      assert.equal(requests.length, 2);
    },
  );

  it(
    "waits out a Retry-After given in seconds",
    { timeout: 10_000 },
    async () => {
      // Read as milliseconds, or ignored for the default backoff, this would
      // retry within half a second.
      script = [problem(503, "unavailable", { "retry-after": "1" }), ok()];
      const started = Date.now();
      await createFeedbackRecord({ client: hub(), body: record });
      assert.equal(requests.length, 2);
      assert.ok(Date.now() - started >= 950, "did not wait the second out");
    },
  );

  it(
    "waits out a Retry-After given as an HTTP-date",
    { timeout: 10_000 },
    async () => {
      // HTTP-dates have whole seconds: two ahead means a wait of one to two.
      const at = new Date(Date.now() + 2_000).toUTCString();
      script = [problem(429, "rate_limited", { "retry-after": at }), ok()];
      const started = Date.now();
      await createFeedbackRecord({ client: hub(), body: record });
      const waited = Date.now() - started;
      assert.equal(requests.length, 2);
      assert.ok(waited >= 900 && waited < 3_000, `waited ${waited}ms`);
    },
  );
});

describe("request bodies across attempts", () => {
  it(
    "the final attempt sends the original body intact",
    { timeout: 10_000 },
    async () => {
      // Attempts before the last send clones; the last sends the original
      // itself. Succeeding only on the third attempt covers both.
      script = [
        problem(429, "rate_limited", NOW),
        problem(429, "rate_limited", NOW),
        ok(),
      ];
      await createFeedbackRecord({ client: hub(), body: record });
      assert.equal(requests.length, 3);
      assert.deepEqual(JSON.parse(requests[2].body), record);
      assert.equal(requests[1].body, requests[0].body);
      assert.equal(requests[2].body, requests[0].body);
    },
  );

  it(
    "sends the body intact with retries off",
    { timeout: 10_000 },
    async () => {
      await createFeedbackRecord({
        client: hub({ maxRetries: 0 }),
        body: record,
      });
      assert.equal(requests.length, 1);
      assert.deepEqual(JSON.parse(requests[0].body), record);
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
    "does not retry a POST on a 503 without Retry-After",
    { timeout: 10_000 },
    async () => {
      // Envoy, and so Istio, answers 503 when the upstream connection resets,
      // which can be after the Hub committed the write. Blank counts as absent.
      for (const headers of [{}, { "retry-after": " " }]) {
        requests.length = 0;
        script = [problem(503, "unavailable", headers), ok()];
        const { response } = await createFeedbackRecord({
          client: hub(),
          body: record,
        });
        assert.equal(requests.length, 1);
        assert.equal(response.status, 503);
      }
    },
  );

  it(
    "does not retry a POST on a 408 without Retry-After",
    { timeout: 10_000 },
    async () => {
      // Envoy answers 408 when its stream idle timeout fires, which can be
      // after the Hub received the whole request.
      script = [problem(408, "timeout"), ok()];
      const { response } = await createFeedbackRecord({
        client: hub(),
        body: record,
      });
      assert.equal(requests.length, 1);
      assert.equal(response.status, 408);
    },
  );

  it(
    "does not treat a malformed Retry-After as a refusal",
    { timeout: 10_000 },
    async () => {
      // Only digits or an HTTP-date count; Number() would read "0x2" as 2.
      for (const value of ["0x2", "garbage", "1e1", "-1"]) {
        requests.length = 0;
        script = [problem(503, "unavailable", { "retry-after": value }), ok()];
        const { response } = await createFeedbackRecord({
          client: hub(),
          body: record,
        });
        assert.equal(requests.length, 1, `retried on Retry-After: ${value}`);
        assert.equal(response.status, 503);
      }
    },
  );

  it(
    "does not retry a PATCH on 500 or after a timeout",
    { timeout: 10_000 },
    async () => {
      script = [problem(500, "internal", NOW)];
      const failed = await updateFeedbackRecord({
        client: hub(),
        path: { id: "r1" },
        body: { value_text: "x" },
      });
      assert.equal(requests.length, 1);
      assert.equal(failed.response.status, 500);

      requests.length = 0;
      script = [HANG, ok()];
      const timedOut = await updateFeedbackRecord({
        client: hub({ timeout: 200 }),
        path: { id: "r1" },
        body: { value_text: "x" },
      });
      assert.equal(requests.length, 1);
      assert.equal(timedOut.error.name, "TimeoutError");
    },
  );

  it(
    "does not read a 409 larger than a problem body for its code",
    { timeout: 10_000 },
    async () => {
      script = [
        {
          status: 409,
          headers: { "content-type": "application/problem+json" },
          body: { code: "tenant_write_conflict", pad: "x".repeat(70_000) },
        },
        ok(),
      ];
      const { response } = await createFeedbackRecord({
        client: hub(),
        body: record,
      });
      assert.equal(requests.length, 1);
      assert.equal(response.status, 409);
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
    "retries a GET on 408, 502, 503 and 504 without Retry-After",
    { timeout: 10_000 },
    async () => {
      for (const status of [408, 502, 503, 504]) {
        requests.length = 0;
        script = [problem(status, "unavailable"), ok()];
        const { response } = await listFeedbackRecords({
          client: hub({ maxRetries: 1 }),
          query: { tenant_id: "org-1" },
        });
        assert.equal(requests.length, 2, `${status} was not retried`);
        assert.equal(response.status, 200);
      }
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
    "returns a response whose Retry-After is longer than its cap",
    { timeout: 10_000 },
    async () => {
      // The server asks for two minutes. Retrying sooner would only be refused
      // again, and waiting would stall the caller, so its answer is returned.
      script = [problem(503, "unavailable", { "retry-after": "120" }), ok()];
      const started = Date.now();
      const { response } = await listFeedbackRecords({
        client: hub(),
        query: { tenant_id: "org-1" },
      });
      assert.equal(requests.length, 1);
      assert.equal(response.status, 503);
      assert.equal(response.headers.get("retry-after"), "120");
      assert.ok(Date.now() - started < 1_000, "waited before returning");
    },
  );
});

describe("unreliable responses", () => {
  it(
    'treats a blank Retry-After as absent, not as "retry now"',
    { timeout: 10_000 },
    async () => {
      // Number("") is 0. Read naively, a blank header would mean an immediate
      // retry; it should mean the default backoff, which starts at 375ms.
      script = [problem(503, "unavailable", { "retry-after": " " }), ok()];
      const started = Date.now();
      await listFeedbackRecords({
        client: hub({ maxRetries: 1 }),
        query: { tenant_id: "org-1" },
      });
      assert.equal(requests.length, 2);
      assert.ok(Date.now() - started >= 350, "retried without backing off");
    },
  );

  it(
    "retries a 503 whose connection drops mid-body",
    { timeout: 10_000 },
    async () => {
      // The realistic version: the response arrives, then the socket goes.
      script = [BROKEN_503, ok({ recovered: true })];
      const { data } = await listFeedbackRecords({
        client: hub({ maxRetries: 1 }),
        query: { tenant_id: "org-1" },
      });
      assert.equal(requests.length, 2);
      assert.deepEqual(data, { recovered: true });
    },
  );

  it(
    "retries a GET whose connection drops before any response, not a POST",
    { timeout: 10_000 },
    async () => {
      script = [RESET, ok({ recovered: true })];
      const { data } = await listFeedbackRecords({
        client: hub({ maxRetries: 1 }),
        query: { tenant_id: "org-1" },
      });
      assert.equal(requests.length, 2);
      assert.deepEqual(data, { recovered: true });

      requests.length = 0;
      script = [RESET, ok()];
      const { error } = await createFeedbackRecord({
        client: hub({ maxRetries: 1 }),
        body: record,
      });
      assert.equal(requests.length, 1);
      assert.ok(error, "a dropped POST must surface, not be re-sent");
    },
  );

  it(
    "retries a GET whose success drops mid-body, not a POST",
    { timeout: 10_000 },
    async () => {
      // The body is read as part of the attempt, so a 200 that never finishes
      // arriving is a failed attempt like any other.
      script = [BROKEN_200, ok({ recovered: true })];
      const { data, response } = await listFeedbackRecords({
        client: hub({ maxRetries: 1 }),
        query: { tenant_id: "org-1" },
      });
      assert.equal(requests.length, 2);
      assert.deepEqual(data, { recovered: true });
      assert.equal(
        response.url,
        `${baseUrl}/v1/feedback-records?tenant_id=org-1`,
      );

      requests.length = 0;
      script = [BROKEN_200, ok()];
      const { error } = await createFeedbackRecord({
        client: hub({ maxRetries: 1 }),
        body: record,
      });
      assert.equal(requests.length, 1);
      assert.ok(error, "a POST whose response broke must surface");
    },
  );

  it(
    "returns a JSON-typed 204 with a body stream as it is",
    { timeout: 10_000 },
    async () => {
      // A runtime may hand over a 204 with an empty body stream. Rebuilding it
      // with a body would throw, and a DELETE that worked would be retried.
      let calls = 0;
      const emptyNoContent = async () => {
        calls += 1;
        const response = new Response(
          new ReadableStream({ start: (c) => c.close() }),
          {
            headers: { "content-type": "application/json" },
          },
        );
        Object.defineProperty(response, "status", { value: 204 });
        return response;
      };
      const response = await createHubFetch({ fetch: emptyNoContent })(
        "http://hub.invalid/v1/feedback-records/r1",
        { method: "DELETE" },
      );
      assert.equal(calls, 1);
      assert.equal(response.status, 204);
    },
  );

  it(
    "still retries when cancelling the discarded body rejects",
    { timeout: 10_000 },
    async () => {
      // The socket version above cannot pin this: the body is cancelled as soon
      // as the headers arrive, before the drop errors it. A real Response over a
      // stream that has already errored makes cancel() reject every time —
      // clean-up failing, which must not turn a retryable 503 into a throw.
      let calls = 0;
      const brokenThenFine = async () => {
        calls += 1;
        if (calls === 1) {
          const body = new ReadableStream({
            start: (controller) =>
              controller.error(new Error("connection reset")),
          });
          return new Response(body, { status: 503 });
        }
        return Response.json({ recovered: true });
      };
      const response = await createHubFetch({
        fetch: brokenThenFine,
        maxRetries: 1,
      })("http://hub.invalid/v1/feedback-records");
      assert.equal(calls, 2);
      assert.equal(response.status, 200);
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
      client: hub({ timeout: 500, maxRetries: 1 }),
      query: { tenant_id: "org-1" },
    });
    assert.equal(requests.length, 2);
    assert.deepEqual(data, { second: true });
  });

  it(
    "times out and retries a GET whose JSON body stalls",
    { timeout: 10_000 },
    async () => {
      script = [SLOW_JSON, ok({ second: true })];
      const { data } = await listFeedbackRecords({
        client: hub({ timeout: 300, maxRetries: 1 }),
        query: { tenant_id: "org-1" },
      });
      assert.equal(requests.length, 2);
      assert.deepEqual(data, { second: true });
    },
  );

  it(
    "leaves a non-JSON body to stream past the timeout",
    { timeout: 10_000 },
    async () => {
      // The timeout covers an attempt up to its headers, then only bodies it
      // reads itself: a stream the caller reads is the caller's to bound.
      script = [SLOW_STREAM];
      const response = await createHubFetch({ timeout: 200 })(
        `${baseUrl}/stream`,
      );
      const text = await response.text();
      assert.equal(text.split("\n").filter(Boolean).length, 6);
    },
  );

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
      // The shortest backoff is 375ms.
      assert.ok(Date.now() - abortedAt < 350, "kept going after the abort");
    },
  );

  it(
    "stops waiting out a backoff as soon as the caller aborts",
    { timeout: 10_000 },
    async () => {
      script = [problem(503, "unavailable", { "retry-after": "5" }), ok()];
      const controller = new AbortController();
      setTimeout(() => controller.abort(), 200);
      const started = Date.now();
      await assert.rejects(
        listFeedbackRecords({
          client: hub(),
          query: { tenant_id: "org-1" },
          signal: controller.signal,
          throwOnError: true,
        }),
        (error) => error.name === "AbortError",
      );
      assert.ok(Date.now() - started < 1_500, "sat through the 5s wait");
      assert.equal(requests.length, 1);
    },
  );
});

describe("garbage collection mid-flight", () => {
  // Node's fetch links a Request's signal to the fetch it started only through
  // weak references. If the wrapper let go of its Requests, a collection while
  // a request is in flight would cut both links, and these would hang.
  const underGc = async (run) => {
    const timer = setInterval(gc, 5);
    try {
      for (let i = 0; i < 5; i++) await run();
    } finally {
      clearInterval(timer);
    }
  };

  it("still times out a hung attempt", { timeout: 10_000 }, async () => {
    await underGc(async () => {
      script = [HANG];
      await assert.rejects(
        createHubFetch({ timeout: 100, maxRetries: 0 })(`${baseUrl}/hang`),
        (error) => error.name === "TimeoutError",
      );
    });
  });

  it("still honours the caller's abort", { timeout: 10_000 }, async () => {
    await underGc(async () => {
      script = [HANG];
      const controller = new AbortController();
      setTimeout(() => controller.abort(), 100);
      await assert.rejects(
        createHubFetch({ timeout: 0 })(`${baseUrl}/hang`, {
          signal: controller.signal,
        }),
        (error) => error.name === "AbortError",
      );
    });
  });

  it(
    "still lets the caller abort a stream it is reading",
    { timeout: 10_000 },
    async () => {
      await underGc(async () => {
        script = [SLOW_STREAM];
        const controller = new AbortController();
        const response = await createHubFetch({ timeout: 0 })(
          `${baseUrl}/stream`,
          { signal: controller.signal },
        );
        setTimeout(() => controller.abort(), 150);
        await assert.rejects(
          response.text(),
          (error) => error.name === "AbortError",
        );
      });
    },
  );
  it(
    "keeps a streamed response's Requests, and lets a read one's go",
    { timeout: 10_000 },
    async () => {
      // Requests carry the API key and body. Once a JSON body is read nothing
      // is in flight, so they must not live as long as the response does.
      const seen = async (path) => {
        let sent;
        const response = await createHubFetch({
          maxRetries: 0,
          fetch: (request) => {
            sent = new WeakRef(request);
            return fetch(request);
          },
        })(`${baseUrl}${path}`);
        return { response, sent };
      };
      script = [ok()];
      const json = await seen("/json");
      script = [SLOW_STREAM];
      const stream = await seen("/stream");
      for (let i = 0; i < 5; i++) {
        gc();
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
      assert.equal(
        json.sent.deref(),
        undefined,
        "read response kept its Request",
      );
      assert.ok(stream.sent.deref(), "streamed response lost its Request");
      await stream.response.text();
      assert.equal(json.response.status, 200);
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
    // Past setTimeout's limit Node would fire after 1ms, aborting every attempt.
    assert.throws(() => createHubFetch({ timeout: 2 ** 31 }), RangeError);
  });

  it("accepts a fractional timeout", { timeout: 10_000 }, async () => {
    // 1.1 * 1000 is 1100.0000000000002; AbortSignal.timeout would reject it.
    const { response } = await listFeedbackRecords({
      client: hub({ timeout: 1.1 * 1000 }),
      query: { tenant_id: "org-1" },
    });
    assert.equal(response.status, 200);
  });
});
