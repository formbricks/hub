/**
 * The generated default client's configuration, loaded through the
 * generator's `runtimeConfigPath` hook (see openapi-ts.config.ts).
 *
 * It exists so the `client` exported from ./generated — the one an operation
 * uses when it is called without `{ client }` — gets the same retries and
 * timeout as `createHubClient`, rather than bare `fetch`.
 */
import type { CreateClientConfig } from "./generated/client.gen";
import { createHubFetch } from "./hub-fetch";

export const createClientConfig: CreateClientConfig = (config) => ({
  ...config,
  fetch: createHubFetch({ fetch: config?.fetch }),
});
