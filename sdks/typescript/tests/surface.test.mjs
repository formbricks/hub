// surface.mjs is what a reviewer reads to decide whether a spec change breaks
// consumers. The check it replaced grepped the source tree for `export const|
// function|type|interface`, which missed `export class`, default exports,
// `export { ... }` and `export *` — and ignored the export map entirely. These
// pin each of those, so the report cannot quietly go back to being decorative.
//
// No network: the script takes two extracted package directories.

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { after, test } from "node:test";

const script = fileURLToPath(
  new URL("../scripts/surface.mjs", import.meta.url),
);

const root = mkdtempSync(path.join(tmpdir(), "surface-"));
after(() => rmSync(root, { recursive: true, force: true }));

let counter = 0;

function writePackage(dir, { manifest, files }) {
  mkdirSync(dir, { recursive: true });
  writeFileSync(
    path.join(dir, "package.json"),
    JSON.stringify(manifest, null, 2),
  );
  for (const [relativePath, contents] of Object.entries(files)) {
    const target = path.join(dir, relativePath);
    mkdirSync(path.dirname(target), { recursive: true });
    writeFileSync(target, contents);
  }
}

function run(publishedPackage, generatedPackage, version = "1.0.0") {
  const pair = path.join(root, `case-${counter++}`);
  const published = path.join(pair, "published");
  const generated = path.join(pair, "generated");
  writePackage(published, publishedPackage);
  writePackage(generated, generatedPackage);

  const result = spawnSync(
    process.execPath,
    [script, published, generated, version],
    { encoding: "utf8" },
  );
  return {
    exitCode: result.status,
    report: result.stdout,
    stderr: result.stderr,
  };
}

const entryManifest = (extra = {}) => ({
  name: "@formbricks/hub",
  version: "1.0.0",
  exports: {
    ".": { import: { types: "./index.d.mts", default: "./index.mjs" } },
  },
  ...extra,
});

test("counts the export shapes a grep over sources misses", () => {
  // Every name here is invisible to `grep '^export (const|function|type|
  // interface)'`, which is exactly why the old check reported a clean surface
  // while FormbricksHub and the default export were being removed.
  const published = {
    manifest: entryManifest(),
    files: {
      "index.d.mts": `
        export declare class FormbricksHub {}
        declare const client: FormbricksHub;
        export default client;
        declare function toFile(): void;
        export { toFile };
        export * from "./errors.mjs";
      `,
      "index.mjs": `
        export class FormbricksHub {}
        export default new FormbricksHub();
        function toFile() {}
        export { toFile };
        export * from "./errors.mjs";
      `,
      "errors.d.mts": "export declare class APIError {}\n",
      "errors.mjs": "export class APIError {}\n",
    },
  };
  // The generated side keeps only APIError, so everything else reads as removed.
  const generated = {
    manifest: entryManifest(),
    files: {
      "index.d.mts": "export declare class APIError {}\n",
      "index.mjs": "export class APIError {}\n",
    },
  };

  const { exitCode, report } = run(published, generated);
  assert.equal(
    exitCode,
    0,
    "a breaking surface is reported, not a job failure",
  );

  const removed = report.slice(report.indexOf("### Removed or renamed"));
  for (const name of ["FormbricksHub", "default", "toFile", "APIError"]) {
    const shouldBeRemoved = name !== "APIError";
    assert.equal(
      removed.includes(`\`${name}\``),
      shouldBeRemoved,
      `${name} should${shouldBeRemoved ? "" : " not"} be listed as removed`,
    );
  }
});

test("compares the export map, not just names", () => {
  const published = {
    manifest: entryManifest({
      exports: {
        ".": { import: "./index.mjs" },
        "./schemas": { import: "./schemas.mjs" },
      },
    }),
    files: {
      "index.mjs": "export const a = 1;\n",
      "schemas.mjs": "export const b = 1;\n",
    },
  };
  const generated = {
    manifest: entryManifest({ exports: { ".": { import: "./index.mjs" } } }),
    files: { "index.mjs": "export const a = 1;\n" },
  };

  const { exitCode, report } = run(published, generated);
  assert.equal(exitCode, 0);
  assert.match(report, /Removed subpaths: `\.\/schemas`/);
  assert.match(report, /Removed entry points: `\.\/schemas`/);
});

test("a long list of removals stays readable in full", () => {
  // The breaking side is the one a reviewer must be able to read to the end;
  // truncating it at 30 with no way to expand hides the damage.
  const names = Array.from({ length: 40 }, (_, index) => `removed${index}`);
  const published = {
    manifest: entryManifest({ exports: { ".": { import: "./index.mjs" } } }),
    files: {
      "index.mjs": names.map((n) => `export const ${n} = 1;`).join("\n"),
    },
  };
  const generated = {
    manifest: entryManifest({ exports: { ".": { import: "./index.mjs" } } }),
    files: { "index.mjs": "export const kept = 1;\n" },
  };

  const { report } = run(published, generated);
  assert.match(report, /<summary>All 40 .* names<\/summary>/);
  for (const name of names) assert.ok(report.includes(name), `${name} missing`);
});

test("a broken exports map fails for our package but not for npm's", () => {
  // Our own packaging bug is fixable in the PR and should turn the job red. The
  // published package is already on npm and nothing in the PR can change it, so
  // failing every preview over it would be noise nobody can action.
  const broken = {
    manifest: entryManifest({ exports: { ".": { import: "./missing.mjs" } } }),
    files: { "index.mjs": "export const a = 1;\n" },
  };
  const fine = {
    manifest: entryManifest({ exports: { ".": { import: "./index.mjs" } } }),
    files: { "index.mjs": "export const a = 1;\n" },
  };

  const publishedBroken = run(broken, fine);
  assert.equal(publishedBroken.exitCode, 0);
  assert.match(publishedBroken.report, /\[!NOTE\]/);
  assert.match(publishedBroken.report, /does not contain: missing\.mjs/);

  const generatedBroken = run(fine, broken);
  assert.equal(generatedBroken.exitCode, 2);
  assert.match(generatedBroken.stderr, /The generated package/);
});

test("usage errors exit 2", () => {
  const result = spawnSync(process.execPath, [script, root], {
    encoding: "utf8",
  });
  assert.equal(result.status, 2);
});
