// The SDK's version is the Hub release's version, stamped from the tag. These
// pin what the stamp accepts and what the check refuses: a malformed tag must
// not become a published version, and a Hub version below npm's latest must
// fail the build rather than the publish.

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  compareVersions,
  refusal,
  versionFromTag,
} from "../scripts/release-version.mjs";

const script = fileURLToPath(
  new URL("../scripts/release-version.mjs", import.meta.url),
);

// `stamp` rewrites the package.json next to the script, so the CLI runs from a
// copy in a temporary package, never against this one.
function scratchPackage(manifest) {
  const dir = mkdtempSync(path.join(tmpdir(), "release-version-"));
  const scripts = path.join(dir, "scripts");
  mkdirSync(scripts);
  copyFileSync(script, path.join(scripts, "release-version.mjs"));
  writeFileSync(
    path.join(dir, "package.json"),
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
  return {
    run(args, env) {
      return spawnSync(
        process.execPath,
        [path.join(scripts, "release-version.mjs"), ...args],
        { encoding: "utf8", env: { PATH: process.env.PATH, ...env } },
      );
    },
    manifest: () =>
      JSON.parse(readFileSync(path.join(dir, "package.json"), "utf8")),
    raw: () => readFileSync(path.join(dir, "package.json"), "utf8"),
  };
}

test("a release tag names its version, with or without a leading v", () => {
  assert.equal(versionFromTag("0.14.0"), "0.14.0");
  assert.equal(versionFromTag("v1.2.3"), "1.2.3");
  assert.equal(versionFromTag("10.20.30"), "10.20.30");
});

test("anything but a plain X.Y.Z is not a release version", () => {
  for (const tag of [
    "0.14",
    "0.14.0-rc.1",
    "0.14.0+build",
    "01.2.3",
    "1.02.3",
    "V1.2.3",
    "release-0.14.0",
    " 0.14.0",
    "0.14.0\n",
    "",
    undefined,
  ]) {
    assert.equal(versionFromTag(tag), undefined, JSON.stringify(tag));
  }
});

test("versions compare numerically, not as strings", () => {
  assert.ok(compareVersions("0.10.0", "0.9.0") > 0);
  assert.ok(compareVersions("0.9.0", "0.13.0") < 0);
  assert.ok(compareVersions("1.0.0", "0.99.99") > 0);
  assert.equal(compareVersions("0.14.0", "0.14.0"), 0);
});

test("a version strictly above npm's latest may publish", () => {
  assert.equal(refusal("0.14.0", "0.13.0"), undefined);
  assert.equal(refusal("1.0.0", "0.13.0"), undefined);
  assert.equal(refusal("0.14.0", ""), undefined, "nothing on npm yet");
  assert.equal(refusal("0.14.0", undefined), undefined);
});

test("a version at or below npm's latest is refused, naming one that works", () => {
  // The Hub 0.9.0 case: below the 0.13.0 Stainless left as latest.
  assert.match(refusal("0.9.0", "0.13.0"), /release Hub 0\.14\.0 or later/);
  assert.match(refusal("0.13.0", "0.13.0"), /already npm's latest/);
  assert.match(refusal("0.14.0", "0.14.0-rc.1"), /not a plain release/);
});

test("stamp writes the tag's version into package.json and prints it", () => {
  const pkg = scratchPackage({
    name: "@formbricks/hub",
    version: "0.0.0-development",
  });
  const result = pkg.run(["stamp"], {
    GITHUB_REF_TYPE: "tag",
    GITHUB_REF_NAME: "v0.14.0",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "0.14.0");
  assert.equal(pkg.manifest().version, "0.14.0");
  assert.equal(pkg.manifest().name, "@formbricks/hub");
  assert.ok(pkg.raw().endsWith("}\n"), "keeps the trailing newline");
});

test("stamp refuses a tag that is not a plain version, and leaves package.json alone", () => {
  const pkg = scratchPackage({
    name: "@formbricks/hub",
    version: "0.0.0-development",
  });
  const result = pkg.run(["stamp"], {
    GITHUB_REF_TYPE: "tag",
    GITHUB_REF_NAME: "0.14.0-rc.1",
  });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /not a plain X\.Y\.Z version/);
  assert.equal(pkg.manifest().version, "0.0.0-development");
});

test("stamp off a tag leaves package.json alone, for a dry run from a branch", () => {
  const pkg = scratchPackage({
    name: "@formbricks/hub",
    version: "0.0.0-development",
  });
  const result = pkg.run(["stamp"], {
    GITHUB_REF_TYPE: "branch",
    GITHUB_REF_NAME: "main",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "0.0.0-development");
  assert.equal(pkg.manifest().version, "0.0.0-development");
});

test("check exits 1 below latest, 0 above it, and 2 on bad usage", () => {
  const pkg = scratchPackage({
    name: "@formbricks/hub",
    version: "0.0.0-development",
  });
  const below = pkg.run(["check", "0.9.0", "0.13.0"], {});
  assert.equal(below.status, 1);
  assert.match(below.stderr, /release Hub 0\.14\.0 or later/);
  assert.equal(pkg.run(["check", "0.14.0", "0.13.0"], {}).status, 0);
  assert.equal(pkg.run(["check", "0.14.0"], {}).status, 0);
  assert.equal(pkg.run(["check", "0.14"], {}).status, 1);
  assert.equal(pkg.run(["check"], {}).status, 2);
  assert.equal(pkg.run([], {}).status, 2);
});
