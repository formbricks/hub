// compare-packed.mjs decides whether a Hub release publishes a new SDK, so the
// ways it can be wrong are expensive in both directions: too sensitive and every
// release burns a version number, too blunt and a changed package silently never
// ships. The previous check compared only src/ and so missed every manifest-only
// change — these pin the cases that motivated replacing it.
//
// No network: the script takes two directories, so the fixtures are built here.

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { after, test } from "node:test";

const script = fileURLToPath(
  new URL("../scripts/compare-packed.mjs", import.meta.url),
);

const root = mkdtempSync(path.join(tmpdir(), "compare-packed-"));
after(() => rmSync(root, { recursive: true, force: true }));

const BASE_MANIFEST = {
  name: "@formbricks/hub",
  version: "0.14.0",
  exports: { ".": { import: "./dist/index.mjs" } },
  peerDependencies: { zod: "^4.0.0" },
  files: ["dist", "src"],
};

let counter = 0;

/** Writes a package pair and returns { exitCode, changes }. */
function compare({ manifest = {}, files = {} } = {}) {
  const pair = path.join(root, `case-${counter++}`);
  const published = path.join(pair, "published");
  const generated = path.join(pair, "generated");

  for (const dir of [published, generated]) {
    mkdirSync(path.join(dir, "dist"), { recursive: true });
    writeFileSync(
      path.join(dir, "package.json"),
      JSON.stringify(BASE_MANIFEST, null, 2),
    );
    writeFileSync(path.join(dir, "dist", "index.mjs"), "export const a = 1;\n");
    writeFileSync(path.join(dir, "README.md"), "# hub\n");
  }

  // The generated side is then mutated into whatever the case is about.
  if (Object.keys(manifest).length > 0) {
    writeFileSync(
      path.join(generated, "package.json"),
      JSON.stringify({ ...BASE_MANIFEST, ...manifest }, null, 2),
    );
  }
  for (const [relativePath, contents] of Object.entries(files)) {
    const target = path.join(generated, relativePath);
    if (contents === null) {
      rmSync(target);
      continue;
    }
    mkdirSync(path.dirname(target), { recursive: true });
    writeFileSync(target, contents);
  }

  const result = spawnSync(process.execPath, [script, published, generated], {
    encoding: "utf8",
  });
  return {
    exitCode: result.status,
    changes: result.stdout.trim().split("\n").filter(Boolean),
  };
}

test("identical packages compare equal, so a release skips", () => {
  const { exitCode, changes } = compare();
  assert.equal(exitCode, 0);
  assert.deepEqual(changes, []);
});

test("a version bump alone is not a package change", () => {
  // The version is the input to a release, not evidence the package differs.
  // Without this, every release would look changed and publish.
  const { exitCode, changes } = compare({ manifest: { version: "9.9.9" } });
  assert.equal(exitCode, 0);
  assert.deepEqual(changes, []);
});

test("an exports-map change counts, with the field named", () => {
  // The case Bhagya reproduced on #127: identical sources, different package.
  const { exitCode, changes } = compare({
    manifest: { exports: { ".": { import: "./dist/index.mjs" } } },
  });
  assert.equal(exitCode, 0, "same exports map must still compare equal");
  assert.deepEqual(changes, []);

  const changed = compare({
    manifest: { exports: { "./schemas": { import: "./dist/schemas.mjs" } } },
  });
  assert.equal(changed.exitCode, 1);
  assert.deepEqual(changed.changes, ["M package.json (fields: exports)"]);
});

test("a peer-range change counts", () => {
  const { exitCode, changes } = compare({
    manifest: { peerDependencies: { zod: "^3.0.0" } },
  });
  assert.equal(exitCode, 1);
  assert.deepEqual(changes, ["M package.json (fields: peerDependencies)"]);
});

test("build output, added files and removed files all count", () => {
  assert.deepEqual(
    compare({ files: { "dist/index.mjs": "export const a = 2;\n" } }),
    {
      exitCode: 1,
      changes: ["M dist/index.mjs"],
    },
  );
  assert.deepEqual(
    compare({ files: { "dist/extra.mjs": "export const b = 1;\n" } }),
    {
      exitCode: 1,
      changes: ["A dist/extra.mjs"],
    },
  );
  assert.deepEqual(compare({ files: { "README.md": null } }), {
    exitCode: 1,
    changes: ["D README.md"],
  });
});

test("bad arguments exit 2, so the workflow can tell them from a difference", () => {
  // The release step branches on this: 2 means "could not compare" and fails the
  // run, while 1 means "differs" and publishes. Conflating them would publish on
  // a broken comparison.
  for (const argv of [[], [root], [root, path.join(root, "nope")]]) {
    const result = spawnSync(process.execPath, [script, ...argv], {
      encoding: "utf8",
    });
    assert.equal(result.status, 2, `argv: ${JSON.stringify(argv)}`);
  }
});
