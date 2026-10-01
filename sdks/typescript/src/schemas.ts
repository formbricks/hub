/**
 * Runtime validators for every Hub request and response shape, generated from
 * ../../openapi.yaml — including the spec's own `minLength`, `maxLength`,
 * `pattern` and enum constraints.
 *
 * Separate entry point on purpose: this is the only part of the package that
 * needs zod, so importing "@formbricks/hub" stays dependency-free and zod is an
 * optional peer.
 */
export * from "./generated/zod.gen";
