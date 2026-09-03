/**
 * @formbricks/hub — the TypeScript client for the Formbricks Hub API.
 *
 * Everything under ./generated is produced from ../../openapi.yaml by
 * @hey-api/openapi-ts and is not committed. This file is the hand-written
 * entry point: it decides the package's public surface and is the place for
 * anything that cannot be generated.
 *
 * Runtime validators live at "@formbricks/hub/schemas" so that importing the
 * client does not pull in zod.
 */

export * from "./generated/sdk.gen";
export * from "./generated/types.gen";
export { createClient, createConfig } from "./generated/client";
export type { Client, ClientOptions, Config } from "./generated/client";

import { createClient, createConfig } from "./generated/client";
import type { Client } from "./generated/client";

/** Options for {@link createHubClient}. */
export interface HubClientOptions {
  /** Hub API key, sent as `Authorization: Bearer <apiKey>`. */
  apiKey: string;
  /**
   * Base URL of the Hub instance, e.g. `https://app.formbricks.com` or
   * `http://localhost:8080`. Required: the Hub is self-hostable, so there is no
   * sensible default.
   */
  baseUrl: string;
}

/**
 * Builds a configured client for a Hub instance.
 *
 * Thin sugar over `createClient(createConfig(...))` — the generated client is
 * always available directly if you need to pass a custom fetch, interceptors or
 * headers. This exists so the common case is one call and so callers do not
 * hand-assemble auth.
 */
export function createHubClient({ apiKey, baseUrl }: HubClientOptions): Client {
  return createClient(
    createConfig({
      baseUrl,
      auth: () => apiKey,
    }),
  );
}
