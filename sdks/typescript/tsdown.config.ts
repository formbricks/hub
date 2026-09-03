import { defineConfig } from "tsdown";

export default defineConfig({
  entry: {
    index: "src/index.ts",
    schemas: "src/schemas.ts",
  },
  format: ["esm", "cjs"],
  dts: true,
  clean: true,
  // zod is an optional peer reachable only from ./schemas; never bundle it.
  external: ["zod"],
});
