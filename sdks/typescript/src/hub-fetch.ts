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
 * An attempt includes reading a JSON body, so a connection that drops or
 * stalls mid-body is retried like one that fails before the response — the
 * Hub's responses are always JSON. Any other body is left to the caller to
 * read as a stream, and the attempt's timeout stops when its headers arrive.
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
// A problem body is a few hundred bytes. A larger 409 body is not one, and is
// not parsed just to look for a code.
const MAX_PROBLEM_BYTES = 64 * 1024;

const IDEMPOTENT_METHODS = new Set(["GET", "HEAD", "OPTIONS", "PUT", "DELETE"]);
const RETRY_ANY_METHOD = new Set([429]);
const RETRY_ANY_METHOD_WITH_RETRY_AFTER = new Set([408, 503]);
const RETRY_IDEMPOTENT_ONLY = new Set([408, 500, 502, 503, 504]);
const RETRYABLE_CONFLICT_CODE = "tenant_write_conflict";

export interface HubFetchOptions {
  /** Retries after the first attempt; `0` turns retries off. Defaults to 2. */
  maxRetries?: number;
  /**
   * Milliseconds each attempt may take — sending the request, waiting for the
   * response and reading a JSON body — applied to every retry independently;
   * `0` turns the timeout off. Defaults to 60 000, at most 2 147 483 647. To
   * bound a call as a whole, pass your own `signal`.
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
    const idempotent = IDEMPOTENT_METHODS.has(original.method.toUpperCase());

    for (let attempt = 0; ; attempt++) {
      const canRetry = attempt < maxRetries;
      const deadline = startDeadline(callerSignal, timeout);

      const request = new Request(canRetry ? original.clone() : original, {
        signal: deadline.signal,
      });
      let result: AttemptResult;
      try {
        result = await read(await baseFetch(request));
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
      // Cleared once the attempt is over, so a finished request does not keep
      // its timer and signals alive for the rest of the timeout.
      deadline.clear();

      const { response, body } = result;
      // Node's fetch links a Request's signal to the fetch it started only
      // through weak references, so these Requests have to outlive a body
      // still to be read: collected mid-flight, they take the timeout and the
      // caller's abort with them, and a hung read waits forever. A body read
      // here has nothing left in flight, so its Requests — credentials and
      // all — are not kept.
      if (!body) retained.set(response, [original, request]);
      if (!canRetry) return response;
      const wait = retryAfter(response);
      if (!isRetryable(response, body, idempotent, wait !== undefined))
        return response;
      if (wait !== undefined && wait > MAX_RETRY_AFTER) return response;

      // Release the connection of a body that was not read. One that has
      // already errored rejects here; that is clean-up failing, not the
      // request, so it must not stop the retry.
      if (!body) await response.body?.cancel().catch(() => {});
      await sleep(wait ?? backoff(attempt), callerSignal);
    }
  };
}

const retained = new WeakMap<Response, Request[]>();

// Statuses a Response may not be constructed with a body for, even an empty
// one; a runtime can still hand them over with an empty body stream.
const NULL_BODY_STATUSES = new Set([101, 103, 204, 205, 304]);

interface AttemptResult {
  response: Response;
  /** The body, when it was JSON and so read as part of the attempt. */
  body?: Uint8Array;
}

/**
 * Reads a JSON body into memory, returning an equivalent response over it.
 * The generated client reads it whole anyway, so this only moves the read
 * inside the attempt. Other bodies are left unread for the caller to stream.
 */
async function read(response: Response): Promise<AttemptResult> {
  const contentType = response.headers.get("content-type") ?? "";
  if (
    !response.body ||
    NULL_BODY_STATUSES.has(response.status) ||
    !contentType.includes("json")
  )
    return { response };

  const body = new Uint8Array(await response.arrayBuffer());
  const copy = new Response(body, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
  // A constructed Response has no URL and was never redirected; report the
  // ones the request actually had.
  Object.defineProperties(copy, {
    url: { value: response.url },
    redirected: { value: response.redirected },
  });
  return { response: copy, body };
}

function isRetryable(
  response: Response,
  body: Uint8Array | undefined,
  idempotent: boolean,
  hasRetryAfter: boolean,
): boolean {
  const { status } = response;
  if (RETRY_ANY_METHOD.has(status)) return true;
  if (RETRY_ANY_METHOD_WITH_RETRY_AFTER.has(status) && hasRetryAfter)
    return true;
  if (RETRY_IDEMPOTENT_ONLY.has(status)) return idempotent;
  // A 409 is retryable only when the API says so: a duplicate-row conflict is
  // final, a serialization conflict with a running purge is not.
  if (status === 409) return problemCode(body) === RETRYABLE_CONFLICT_CODE;
  return false;
}

/** The problem body's `code`, if the body is a problem body. */
function problemCode(body: Uint8Array | undefined): string | undefined {
  if (!body || body.byteLength > MAX_PROBLEM_BYTES) return undefined;
  try {
    const problem: unknown = JSON.parse(new TextDecoder().decode(body));
    if (problem !== null && typeof problem === "object" && "code" in problem) {
      return typeof problem.code === "string" ? problem.code : undefined;
    }
  } catch {
    // Not JSON after all: not a problem body.
  }
  return undefined;
}

/**
 * Milliseconds a `Retry-After` header asks for: delay-seconds, which are
 * digits only, or an HTTP-date, which starts with a day name. Anything else —
 * blank, `0x10`, `1e3`, `-1` — is treated as absent rather than guessed at.
 */
function retryAfter(response: Response): number | undefined {
  const header = response.headers.get("retry-after")?.trim();
  if (!header) return undefined;
  if (/^\d+$/.test(header)) return Number(header) * 1000;
  if (!/^[A-Za-z]{3}/.test(header)) return undefined;
  const date = Date.parse(header);
  return Number.isNaN(date) ? undefined : Math.max(0, date - Date.now());
}

/** Exponential backoff with up to 25% jitter, as in the SDK this replaced. */
function backoff(attempt: number): number {
  const delay = Math.min(INITIAL_RETRY_DELAY * 2 ** attempt, MAX_RETRY_DELAY);
  return delay * (1 - Math.random() * 0.25);
}

/** The caller's signal, plus this attempt's timeout unless that is off. */
function startDeadline(
  callerSignal: AbortSignal,
  timeout: number,
): { signal: AbortSignal; clear(): void } {
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
