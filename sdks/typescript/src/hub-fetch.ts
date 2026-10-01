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
 * 60s per attempt, exponential backoff from 0.5s to 8s with jitter — so moving
 * to it changes nothing a caller would notice. What gets retried does change,
 * on purpose. Stainless re-sent any request that failed with a 5xx, POSTs
 * included, and the Hub has no idempotency keys, so a retried create could
 * write the same record twice. Here a request is only re-sent when that
 * cannot happen:
 *
 *   - any method, on a status meaning the server did not act on it — 408,
 *     429, 503 — or on a 409 whose problem body carries the code the API
 *     documents as retryable, `tenant_write_conflict`;
 *   - idempotent methods only, additionally on statuses and failures after
 *     which the request may already have taken effect — 500, 502, 504, a
 *     network error, an attempt that timed out.
 *
 * Every other response is returned as it is, and a request the caller aborted
 * is never retried.
 */

/** Retries after the first attempt, unless overridden. */
export const DEFAULT_MAX_RETRIES = 2;
/** Milliseconds each attempt may take, unless overridden. */
export const DEFAULT_TIMEOUT = 60_000;

const INITIAL_RETRY_DELAY = 500;
const MAX_RETRY_DELAY = 8_000;
// A Retry-After longer than this is not waited out: the default backoff
// applies instead, so one response cannot stall a caller for minutes.
const MAX_RETRY_AFTER = 60_000;

const IDEMPOTENT_METHODS = new Set(["GET", "HEAD", "OPTIONS", "PUT", "DELETE"]);
const RETRY_ANY_METHOD = new Set([408, 429, 503]);
const RETRY_IDEMPOTENT_ONLY = new Set([500, 502, 504]);
const RETRYABLE_CONFLICT_CODE = "tenant_write_conflict";

export interface HubFetchOptions {
  /** Retries after the first attempt; `0` turns retries off. Defaults to 2. */
  maxRetries?: number;
  /**
   * Milliseconds each attempt may take, including reading the response body,
   * applied to every retry independently; `0` turns the timeout off. Defaults
   * to 60 000. To bound a call as a whole, pass your own `signal`.
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
  if (!Number.isFinite(timeout) || timeout < 0) {
    throw new RangeError(
      `timeout must be a non-negative number of milliseconds, got ${timeout}`,
    );
  }

  return async function hubFetch(input, init) {
    const baseFetch = options.fetch ?? globalThis.fetch;

    // Never sent itself: each attempt sends a clone, because a request body can
    // be read only once and a retry has to send it again.
    const original = new Request(input, init);
    const callerSignal = original.signal;
    const idempotent = IDEMPOTENT_METHODS.has(original.method.toUpperCase());

    for (let attempt = 0; ; attempt++) {
      const canRetry = attempt < maxRetries;
      const signal =
        timeout > 0
          ? AbortSignal.any([callerSignal, AbortSignal.timeout(timeout)])
          : callerSignal;

      let response: Response;
      try {
        response = await baseFetch(new Request(original.clone(), { signal }));
      } catch (error) {
        // The caller's own cancellation is final. Otherwise this was a network
        // failure or the attempt timed out, and the request may have reached
        // the server — only an idempotent one can safely be sent again.
        if (callerSignal.aborted || !canRetry || !idempotent) throw error;
        await sleep(backoff(attempt), callerSignal);
        continue;
      }

      if (!canRetry || !(await isRetryable(response, idempotent)))
        return response;

      const delay = retryAfter(response) ?? backoff(attempt);
      // Release the connection rather than leave it to garbage collection.
      await response.body?.cancel();
      await sleep(delay, callerSignal);
    }
  };
}

async function isRetryable(
  response: Response,
  idempotent: boolean,
): Promise<boolean> {
  const { status } = response;
  if (RETRY_ANY_METHOD.has(status)) return true;
  if (RETRY_IDEMPOTENT_ONLY.has(status)) return idempotent;
  // A 409 is retryable only when the API says so: a duplicate-row conflict is
  // final, a serialization conflict with a running purge is not.
  if (status === 409)
    return (await problemCode(response)) === RETRYABLE_CONFLICT_CODE;
  return false;
}

/** The problem body's `code`, read from a clone so the caller can still read the body. */
async function problemCode(response: Response): Promise<string | undefined> {
  try {
    const body: unknown = await response.clone().json();
    if (body !== null && typeof body === "object" && "code" in body) {
      return typeof body.code === "string" ? body.code : undefined;
    }
  } catch {
    // Not JSON: not a problem body, so not a documented retryable conflict.
  }
  return undefined;
}

/** Milliseconds requested by a `Retry-After` header, if usable. */
function retryAfter(response: Response): number | undefined {
  const header = response.headers.get("retry-after");
  if (header === null) return undefined;

  const seconds = Number(header);
  const ms = Number.isFinite(seconds)
    ? seconds * 1000
    : Date.parse(header) - Date.now();
  return Number.isFinite(ms) && ms >= 0 && ms <= MAX_RETRY_AFTER
    ? ms
    : undefined;
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
