import { defineConfig } from "@hey-api/openapi-ts";

/**
 * Generates the @formbricks/hub client from the Hub's own OpenAPI spec.
 *
 * The output is NOT committed — see .gitignore and AGENTS.md. It is produced in
 * CI and published to npm on a Hub release, and reproducibly regenerated here
 * with `pnpm generate`. The generator version is pinned exactly in package.json
 * so a bump is a deliberate, reviewable change rather than a silent one.
 */
export default defineConfig({
  input: "../../openapi.yaml",
  output: {
    path: "./src/generated",
    // Keep formatting out of generation so the output is a pure function of
    // (spec, generator version). The publish pipeline's skip-if-unchanged check
    // compares the packed package byte for byte, so anything nondeterministic
    // here would make every release look changed.
    postProcess: [],
  },
  plugins: [
    {
      name: "@hey-api/client-fetch",
      // The generated client has no retries or timeout of its own; this hook
      // gives its default client the ones in src/hub-fetch.ts. Extensionless,
      // to match every other import the generator writes.
      runtimeConfigPath: "./src/client-config",
    },
    "@hey-api/typescript",
    // Default emits one exported function per operation (not a class), which
    // keeps tree-shaking useful and matches how the spec reads.
    "@hey-api/sdk",
    // Runtime validators. Exposed through the ./schemas entry point only, so the
    // default entry stays dependency-free and zod remains an optional peer.
    "zod",
  ],
});
