/**
 * Retries and a per-attempt timeout for every Hub request.
 *
 * The generated client is a thin layer over `fetch` and has neither, so this
 * wraps the `fetch` it calls. `createHubClient` uses it, and so does the
 * generated default client, through the generator's `runtimeConfigPath` hook
 * (see ./client-config.ts). Hand-written: everything under ./generated is
 * overwritten on each run.
 *
 * The defaults match the Stainless-generated SDK this replaced — 2 retries,
 * 60s per attempt, exponential backoff from 0.5s to 8s with jitter. What gets
 * retried does not, on purpose. Stainless re-sent any request that failed with
 * a 5xx, POSTs included, and the Hub has no idempotency keys: a create the Hub
 * had already applied would come back as a 409 conflict, or — for a webhook,
 * which has no natural key — be created twice. Here a request is only re-sent
 * when that cannot happen:
 *
 *   - any method, on a response that says the server refused it — 429, a 408
 *     or 503 that carries `Retry-After` — or on a 409 whose problem body
 *     carries the code the API documents as safe to retry,
 *     `tenant_write_conflict`;
 *   - idempotent methods only, additionally after failures that leave open
 *     whether the request took effect — 500, 502, 504, a 408 or 503 without
 *     `Retry-After`, a network error, an attempt that timed out.
 *
 * A bare 408 or 503 is ambiguous because proxies send them after forwarding
 * the request: Envoy, and so Istio, answers 503 when the upstream connection
 * resets and 408 when its stream idle timeout fires, either of which can be
 * after the Hub committed a write. The Hub itself sends neither with
 * `Retry-After`.
 *
 * Whether to retry is decided from the status and headers; the body of a
 * response that is retried is discarded unread, except a 409's, of which at
 * most 64 KB is read for its code. The timeout runs until the returned
 * response's body has been read, whatever its type. An idempotent request
 * reads a JSON body before it is returned, so a connection that drops or
 * stalls mid-body is retried like one that fails before the response.
 *
 * Every other response is returned as it is, so is a response whose
 * `Retry-After` asks for longer than this will wait, and a request the caller
 * aborted is never retried. Idempotency keys would make every retry safe, as
 * they do for Stripe's SDK; until the Hub has them, an ambiguous failure is the
 * caller's to retry, as gRPC advises for UNAVAILABLE on non-idempotent calls.
 */

/** Retries after the first attempt, unless overridden. */
export const DEFAULT_MAX_RETRIES = 2;
/** Milliseconds each attempt may take, unless overridden. */
export const DEFAULT_TIMEOUT = 60_000;

const INITIAL_RETRY_DELAY = 500;
const MAX_RETRY_DELAY = 8_000;
// A response asking for a longer wait than this is returned, not retried
// early: the server said when, and sooner would only be refused again.
const MAX_RETRY_AFTER = 60_000;
// The largest delay setTimeout honours; past it, Node fires after 1ms.
const MAX_TIMEOUT = 2_147_483_647;
// A problem body is a few hundred bytes. A 409 body larger than this is not
// one, and is not read further just to look for a code.
const MAX_PROBLEM_BYTES = 64 * 1024;

const IDEMPOTENT_METHODS = new Set(["GET", "HEAD", "OPTIONS", "PUT", "DELETE"]);
const RETRY_ANY_METHOD = new Set([429]);
const RETRY_ANY_METHOD_WITH_RETRY_AFTER = new Set([408, 503]);
const RETRY_IDEMPOTENT_ONLY = new Set([408, 500, 502, 503, 504]);
const RETRYABLE_CONFLICT_CODE = "tenant_write_conflict";

// Statuses a Response may not be constructed with a body for, even an empty
// one; a runtime can still hand them over with an empty body stream.
const NULL_BODY_STATUSES = new Set([101, 103, 204, 205, 304]);
// What the Response constructor accepts as a reason phrase (RFC 9112 §4).
const REASON_PHRASE = /^[\t\x20-\x7e\x80-\xff]*$/;
// IMF-fixdate (RFC 9110 §5.6.7), the one form senders must generate. The two
// obsolete forms are treated as absent rather than parsed leniently.
const IMF_FIXDATE =
  /^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{2} (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{4} \d{2}:\d{2}:\d{2} GMT$/;

export interface HubFetchOptions {
  /** Retries after the first attempt; `0` turns retries off. Defaults to 2. */
  maxRetries?: number;
  /**
   * Milliseconds each attempt may take — sending the request, waiting for the
   * response and reading its body — applied to every retry independently;
   * `0` turns the timeout off, as a long-lived stream needs. Defaults to
   * 60 000, at most 2 147 483 647. To bound a call as a whole, pass your own
   * `signal`.
   */
  timeout?: number;
  /** The `fetch` to wrap. Defaults to the global `fetch`, looked up per call. */
  fetch?: typeof fetch;
}

/** A `fetch` that applies the Hub's retry policy and per-attempt timeout. */
export function createHubFetch(options: HubFetchOptions = {}): typeof fetch {
  const maxRetries = options.maxRetries ?? DEFAULT_MAX_RETRIES;
  const timeout = options.timeout ?? DEFAULT_TIMEOUT;

  if (!Number.isInteger(maxRetries) || maxRetries < 0) {
    throw new RangeError(
      `maxRetries must be a non-negative integer, got ${maxRetries}`,
    );
  }
  if (!(timeout >= 0 && timeout <= MAX_TIMEOUT)) {
    throw new RangeError(
      `timeout must be between 0 and ${MAX_TIMEOUT} milliseconds, got ${timeout}`,
    );
  }

  return async function hubFetch(input, init) {
    const baseFetch = options.fetch ?? globalThis.fetch;

    // Kept unsent while another attempt may follow: those attempts send a
    // clone, because a request body can be read only once and a retry has to
    // send it again. The last attempt sends the original, so with retries off
    // the body is never copied.
    const original = new Request(input, init);
    const callerSignal = original.signal;
    // The caller's own signal reaches `original` through `input` when that is
    // a Request — the generated client always passes one — so it is held too.
    const held = input instanceof Request ? [input, original] : [original];
    const idempotent = IDEMPOTENT_METHODS.has(original.method.toUpperCase());

    for (let attempt = 0; ; attempt++) {
      const canRetry = attempt < maxRetries;
      const deadline = startDeadline(callerSignal, timeout);
      // Used after every await below, so it stays reachable while in flight.
      const request = new Request(canRetry ? original.clone() : original, {
        signal: deadline.signal,
      });

      let response: Response;
      let retryIn: number | undefined;
      let read = false;
      try {
        response = await baseFetch(request);
        if (canRetry) {
          const wait = retryAfter(response);
          if (await isRetryable(response, idempotent, wait)) {
            retryIn = wait ?? backoff(attempt);
          } else if (
            idempotent &&
            original.method !== "HEAD" &&
            isJson(response) &&
            canRebuild(response)
          ) {
            // Read inside the attempt, so a body that breaks off is retried.
            const chunks = await readAll(response.body);
            response = rebuild(response, streamOf(chunks));
            read = true;
          }
        }
      } catch (error) {
        deadline.clear();
        // The caller's own cancellation is final. Otherwise the connection
        // failed or the attempt timed out, before the response or while its
        // body was read, and the request may have reached the server — only
        // an idempotent one can safely be sent again.
        if (callerSignal.aborted || !canRetry || !idempotent) throw error;
        await sleep(backoff(attempt), callerSignal);
        continue;
      }

      if (retryIn !== undefined) {
        deadline.clear();
        // Released unread. A body that has already errored rejects here; that
        // is clean-up failing, not the request, so it must not stop the retry.
        await response.body?.cancel().catch(() => {});
        await sleep(retryIn, callerSignal);
        continue;
      }

      // An attempt that sent a clone never sent the original: drop its copy
      // of the body now rather than when the response is collected.
      if (canRetry) void original.body?.cancel().catch(() => {});
      if (read) {
        deadline.clear();
        return response;
      }
      return withDeadline(response, deadline, [...held, request]);
    }
  };
}

type Deadline = { signal: AbortSignal; clear(): void };

/** The caller's signal, plus this attempt's timeout unless that is off. */
function startDeadline(callerSignal: AbortSignal, timeout: number): Deadline {
  if (timeout === 0) return { signal: callerSignal, clear() {} };

  const controller = new AbortController();
  const timer = setTimeout(
    () =>
      controller.abort(
        new DOMException("The operation timed out.", "TimeoutError"),
      ),
    timeout,
  );
  // Like AbortSignal.timeout, do not keep a Node process alive on its own.
  (timer as { unref?: () => void }).unref?.();
  return {
    signal: AbortSignal.any([callerSignal, controller.signal]),
    clear: () => clearTimeout(timer),
  };
}

// Requests of a response returned as it is, kept for as long as the response
// or its body is — a caller may keep only the body, as a stream.
const retained = new WeakMap<object, Request[]>();

/**
 * The response, with its attempt's deadline running until its body has been
 * read, cancelled or has failed.
 *
 * Node's fetch links a Request's signal to the fetch it started only through
 * weak references, so the attempt's Requests are held until then too:
 * collected while the body is still arriving, they would take the timeout and
 * the caller's abort with them, and a stalled read would wait forever.
 */
function withDeadline(
  response: Response,
  deadline: Deadline,
  requests: Request[],
): Response {
  if (!response.body || NULL_BODY_STATUSES.has(response.status)) {
    deadline.clear();
    return response;
  }
  // A status line the Response constructor would reject: returned as it is,
  // its deadline left to run its course.
  if (!canRebuild(response)) {
    retained.set(response, requests);
    retained.set(response.body, requests);
    return response;
  }

  const reader = response.body.getReader();
  const done = () => {
    deadline.clear();
    requests.length = 0;
  };
  const body = new ReadableStream<Uint8Array>({
    async pull(controller) {
      try {
        const chunk = await reader.read();
        if (chunk.done) {
          done();
          controller.close();
        } else {
          controller.enqueue(chunk.value);
        }
      } catch (error) {
        done();
        controller.error(error);
      }
    },
    cancel(reason) {
      done();
      return reader.cancel(reason);
    },
  });
  return rebuild(response, body);
}

/** `application/json` or a `+json` type, as media types match: by essence, case-insensitively. */
function isJson(response: Response): boolean {
  const essence = (response.headers.get("content-type") ?? "")
    .split(";")[0]!
    .trim()
    .toLowerCase();
  return essence === "application/json" || essence.endsWith("+json");
}

/** Whether the Response constructor accepts this status line with a body. */
function canRebuild(response: Response): boolean {
  const { status, statusText } = response;
  return (
    status >= 200 &&
    status <= 599 &&
    !NULL_BODY_STATUSES.has(status) &&
    REASON_PHRASE.test(statusText)
  );
}

/**
 * An equivalent response over another body. What still differs from one fetch
 * returns: `type` is "default", and its headers are mutable.
 */
function rebuild(response: Response, body: ReadableStream<Uint8Array>) {
  const copy = new Response(body, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
  return withOrigin(copy, response.url, response.redirected);
}

/**
 * A constructed Response has no URL and was never redirected: report the ones
 * the request actually had, on its clones too.
 */
function withOrigin(copy: Response, url: string, redirected: boolean) {
  const clone = copy.clone.bind(copy);
  return Object.defineProperties(copy, {
    url: { value: url },
    redirected: { value: redirected },
    clone: { value: () => withOrigin(clone(), url, redirected) },
  });
}

/**
 * A body's chunks as they arrived. Not joined: whoever reads the rebuilt body
 * joins them once, as it would have the original's.
 */
async function readAll(
  body: ReadableStream<Uint8Array> | null,
): Promise<Uint8Array[]> {
  const chunks: Uint8Array[] = [];
  if (!body) return chunks;
  const reader = body.getReader();
  for (;;) {
    const chunk = await reader.read();
    if (chunk.done) return chunks;
    chunks.push(chunk.value);
  }
}

/** A stream of chunks already in memory, without copying them. */
function streamOf(chunks: Uint8Array[]): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(chunk);
      controller.close();
    },
  });
}

async function isRetryable(
  response: Response,
  idempotent: boolean,
  wait: number | undefined,
): Promise<boolean> {
  const { status } = response;
  if (wait !== undefined && wait > MAX_RETRY_AFTER) return false;
  if (RETRY_ANY_METHOD.has(status)) return true;
  if (RETRY_ANY_METHOD_WITH_RETRY_AFTER.has(status) && wait !== undefined)
    return true;
  if (RETRY_IDEMPOTENT_ONLY.has(status)) return idempotent;
  // A 409 is retryable only when the API says so: a duplicate-row conflict is
  // final, a serialization conflict with a running purge is not.
  if (status === 409)
    return (await problemCode(response)) === RETRYABLE_CONFLICT_CODE;
  return false;
}

/**
 * The problem body's `code`, read from a clone — so the caller can still read
 * the body — and only if the body is JSON and no larger than a problem body.
 */
async function problemCode(response: Response): Promise<string | undefined> {
  if (!response.body || !isJson(response)) return undefined;
  const reader = response.clone().body!.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      size += chunk.value.byteLength;
      if (size > MAX_PROBLEM_BYTES) return undefined;
      chunks.push(chunk.value);
    }
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, offset);
      offset += chunk.byteLength;
    }
    const problem: unknown = JSON.parse(new TextDecoder().decode(bytes));
    if (problem !== null && typeof problem === "object" && "code" in problem) {
      return typeof problem.code === "string" ? problem.code : undefined;
    }
    return undefined;
  } catch {
    // Not JSON after all, or the body failed: not a readable problem body.
    return undefined;
  } finally {
    // Not awaited: a branch of a cloned body settles its cancel only once the
    // other branch is read or cancelled too, which is up to the caller.
    void reader.cancel().catch(() => {});
  }
}

/**
 * Milliseconds a `Retry-After` header asks for: delay-seconds, which are
 * digits only, or an IMF-fixdate. Anything else — blank, `0x10`, `1e3`, `-1`,
 * an obsolete date form — is treated as absent rather than guessed at.
 */
function retryAfter(response: Response): number | undefined {
  const header = response.headers.get("retry-after")?.trim();
  if (!header) return undefined;
  if (/^\d+$/.test(header)) return Number(header) * 1000;
  if (!IMF_FIXDATE.test(header)) return undefined;
  const date = Date.parse(header);
  return Number.isNaN(date) ? undefined : Math.max(0, date - Date.now());
}

/** Exponential backoff with up to 25% jitter, as in the SDK this replaced. */
function backoff(attempt: number): number {
  const delay = Math.min(INITIAL_RETRY_DELAY * 2 ** attempt, MAX_RETRY_DELAY);
  return delay * (1 - Math.random() * 0.25);
}

/** Waits, unless the caller aborts first. */
function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(signal.reason);
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal.addEventListener("abort", onAbort, { once: true });
  });
}
