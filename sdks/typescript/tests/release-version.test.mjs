// The SDK's version is the Hub release's version, stamped from the tag. These
// pin what the stamp accepts and writes, how a release's npm dist-tag is
// planned — `latest`, or `release-X.Y` for a backport — and what the publish job
// checks again on its own: a malformed tag must not become a published version,
// a backport must not move `latest`, and a version npm already has must fail the
// build rather than the publish.

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
  isPlainVersion,
  lineTag,
  parseVersions,
  plan,
  RELEASE,
  versionFromTag,
} from "../scripts/release-version.mjs";

const script = fileURLToPath(
  new URL("../scripts/release-version.mjs", import.meta.url),
);
const realManifest = readFileSync(
  fileURLToPath(new URL("../package.json", import.meta.url)),
  "utf8",
);

// `stamp` rewrites the package.json next to the script, so the CLI runs from a
// copy in a temporary package, never against this one.
function scratchPackage(manifestText) {
  const dir = mkdtempSync(path.join(tmpdir(), "release-version-"));
  const scripts = path.join(dir, "scripts");
  mkdirSync(scripts);
  copyFileSync(script, path.join(scripts, "release-version.mjs"));
  writeFileSync(path.join(dir, "package.json"), manifestText);
  return {
    dir,
    run(args, env = {}) {
      return spawnSync(
        process.execPath,
        [path.join(scripts, "release-version.mjs"), ...args],
        { encoding: "utf8", env: { PATH: process.env.PATH, ...env } },
      );
    },
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
  // A version, as opposed to a tag, carries no `v`.
  assert.equal(isPlainVersion("0.14.0"), true);
  assert.equal(isPlainVersion("v0.14.0"), false);
});

test("versions compare numerically, not as strings", () => {
  assert.ok(compareVersions("0.10.0", "0.9.0") > 0);
  assert.ok(compareVersions("0.9.0", "0.13.0") < 0);
  assert.ok(compareVersions("1.0.0", "0.99.99") > 0);
  assert.equal(compareVersions("0.14.0", "0.14.0"), 0);
});

// What Stainless left on npm: 0.2.0 through 0.13.0.
const STAINLESS = ["0.2.0", "0.9.0", "0.10.0", "0.11.0", "0.12.0", "0.13.0"];

test("a version above every published release publishes as latest", () => {
  assert.deepEqual(plan("0.14.0", STAINLESS), { tag: "latest" });
  assert.deepEqual(plan("1.0.0", STAINLESS), { tag: "latest" });
  assert.deepEqual(plan("0.14.0", []), { tag: "latest" }, "first publish");
});

test("a backport publishes under its line's tag, leaving latest alone", () => {
  // A 0.14.1 security fix released after 0.15.0.
  const published = [...STAINLESS, "0.14.0", "0.15.0"];
  assert.deepEqual(plan("0.14.1", published), { tag: "release-0.14" });
  assert.equal(lineTag("0.14.1"), "release-0.14");
  // And the next patch on that line follows it.
  assert.deepEqual(plan("0.14.2", [...published, "0.14.1"]), {
    tag: "release-0.14",
  });
});

test("a version npm already has is refused", () => {
  // The Hub 0.9.0 case: Stainless published an SDK 0.9.0.
  assert.match(plan("0.9.0", STAINLESS).refused, /already on npm/);
  assert.match(plan("0.13.0", STAINLESS).refused, /already on npm/);
});

test("a backport older than one already on its line is refused", () => {
  const published = [...STAINLESS, "0.14.0", "0.14.2", "0.15.0"];
  const { refused } = plan("0.14.1", published);
  assert.match(refused, /0\.14\.2 is already published/);
  assert.match(refused, /release-0\.14 tag backwards/);
});

test("published prereleases do not decide the ordering", () => {
  assert.deepEqual(plan("0.14.0", [...STAINLESS, "0.15.0-rc.1"]), {
    tag: "latest",
  });
});

test("plan refuses a version that is not plain, including a v-prefixed one", () => {
  assert.match(plan("v0.14.0", STAINLESS).refused, /not a plain X\.Y\.Z/);
  assert.match(plan("0.14", STAINLESS).refused, /not a plain X\.Y\.Z/);
});

test("the published-versions list is read in either of npm's shapes", () => {
  assert.deepEqual(parseVersions('["0.1.0","0.2.0"]'), ["0.1.0", "0.2.0"]);
  assert.deepEqual(parseVersions('[["0.1.0","0.2.0"]]'), ["0.1.0", "0.2.0"]);
  assert.deepEqual(parseVersions('"0.1.0"'), ["0.1.0"]);
  assert.deepEqual(parseVersions("  \n"), []);
  assert.throws(() => parseVersions('{"a":1}'));
});

test("stamp changes only the version line of the real package.json", () => {
  const pkg = scratchPackage(realManifest);
  const result = pkg.run(["stamp"], {
    GITHUB_REF_TYPE: "tag",
    GITHUB_REF_NAME: "v0.14.0",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "0.14.0");

  // Every field survives, in order, and the file differs from the committed
  // one in exactly one line.
  const before = JSON.parse(realManifest);
  const after = JSON.parse(pkg.raw());
  assert.deepEqual(Object.keys(after), Object.keys(before));
  assert.deepEqual({ ...after, version: before.version }, before);
  assert.equal(after.version, "0.14.0");
  const beforeLines = realManifest.split("\n");
  const afterLines = pkg.raw().split("\n");
  assert.equal(afterLines.length, beforeLines.length);
  const changed = afterLines.filter(
    (line, index) => line !== beforeLines[index],
  );
  assert.deepEqual(changed, ['  "version": "0.14.0",']);
});

test("stamp refuses a tag that is not a plain version, and leaves package.json alone", () => {
  const pkg = scratchPackage(realManifest);
  const result = pkg.run(["stamp"], {
    GITHUB_REF_TYPE: "tag",
    GITHUB_REF_NAME: "0.14.0-rc.1",
  });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /not a plain X\.Y\.Z version/);
  assert.equal(pkg.raw(), realManifest);
});

test("stamp off a tag leaves package.json alone, for a dry run from a branch", () => {
  const pkg = scratchPackage(realManifest);
  const result = pkg.run(["stamp"], {
    GITHUB_REF_TYPE: "branch",
    GITHUB_REF_NAME: "main",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), JSON.parse(realManifest).version);
  assert.equal(pkg.raw(), realManifest);
});

test("plan prints the dist-tag, exits 1 on a refusal and 2 on bad input", () => {
  const pkg = scratchPackage(realManifest);
  const versions = path.join(pkg.dir, "versions.json");
  writeFileSync(versions, JSON.stringify([...STAINLESS, "0.14.0", "0.15.0"]));

  const latest = pkg.run(["plan", "0.16.0", versions]);
  assert.equal(latest.status, 0, latest.stderr);
  assert.equal(latest.stdout.trim(), "latest");

  const backport = pkg.run(["plan", "0.14.1", versions]);
  assert.equal(backport.status, 0, backport.stderr);
  assert.equal(backport.stdout.trim(), "release-0.14");

  const taken = pkg.run(["plan", "0.13.0", versions]);
  assert.equal(taken.status, 1);
  assert.equal(taken.stdout, "", "a refusal names no tag");
  assert.match(taken.stderr, /already on npm/);

  writeFileSync(versions, "not json");
  assert.equal(pkg.run(["plan", "0.16.0", versions]).status, 2);
  assert.equal(pkg.run(["plan", "0.16.0"]).status, 2);
  assert.equal(pkg.run([]).status, 2);
});

test("importing the script runs nothing, whatever argv[1] is", () => {
  const result = spawnSync(
    process.execPath,
    [
      "--input-type=module",
      "-e",
      `await import(${JSON.stringify(script)}); console.log("imported");`,
      "/nonexistent/argv1",
    ],
    { encoding: "utf8" },
  );
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "imported");
});

// The publish job checks the version and dist-tag against the tag a second
// time, with its own copy of the pattern (it has no checkout), and refuses what
// the build job — which runs third-party code — could have rewritten along with
// its own reported outputs.
const workflow = readFileSync(
  fileURLToPath(
    new URL("../../../.github/workflows/publish-sdk.yml", import.meta.url),
  ),
  "utf8",
);

test("the publish job checks the tag with the same pattern as this script", () => {
  const match = /const RELEASE = \/(.+)\/;/.exec(workflow);
  assert.ok(match, "publish-sdk.yml no longer defines RELEASE");
  assert.equal(match[1], RELEASE.source);
});

// The validation step's own script, as it runs in the publish job.
function validationScript() {
  const start = workflow.indexOf("node --input-type=commonjs - <<'NODE'\n");
  const end = workflow.indexOf("\n          NODE\n", start);
  assert.ok(start >= 0 && end > start, "validation step not found");
  return workflow.slice(workflow.indexOf("\n", start) + 1, end);
}

function validate(version, env) {
  const dir = mkdtempSync(path.join(tmpdir(), "publish-validation-"));
  writeFileSync(
    path.join(dir, "package.json"),
    JSON.stringify({ name: "@formbricks/hub", version }),
  );
  return spawnSync(process.execPath, ["--input-type=commonjs", "-"], {
    cwd: dir,
    input: validationScript(),
    encoding: "utf8",
    env: {
      PATH: process.env.PATH,
      EXPECTED_NAME: "@formbricks/hub",
      EXPECTED_VERSION: version,
      EXPECTED_DIST_TAG: "latest",
      GITHUB_REF_TYPE: "tag",
      GITHUB_REF_NAME: version,
      ...env,
    },
  });
}

test("the publish job accepts the tag's version under latest or its own line", () => {
  assert.equal(validate("0.14.0").status, 0);
  const backport = validate("0.14.1", { EXPECTED_DIST_TAG: "release-0.14" });
  assert.equal(backport.status, 0, backport.stderr);
});

test("the publish job refuses a version the build job reported but the tag does not name", () => {
  // A compromised build job rewrites the manifest and its own output alike.
  const result = validate("999.0.0", { GITHUB_REF_NAME: "0.14.0" });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /the release tag is 0\.14\.0/);
});

test("the publish job refuses an artifact whose version is not the one the build reported", () => {
  const result = validate("0.14.0", { EXPECTED_VERSION: "0.14.1" });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /the build job reported 0\.14\.1/);
});

test("the publish job refuses a dist-tag the tag does not allow", () => {
  for (const tag of ["release-0.13", "next", "", "latest "]) {
    const result = validate("0.14.1", { EXPECTED_DIST_TAG: tag });
    assert.equal(result.status, 1, JSON.stringify(tag));
    assert.match(result.stderr, /can only publish under/);
  }
});

test("the publish job refuses a run that is not for a plain version tag", () => {
  const branch = validate("0.14.0", {
    GITHUB_REF_TYPE: "branch",
    GITHUB_REF_NAME: "main",
  });
  assert.equal(branch.status, 1);
  assert.match(branch.stderr, /not a release tag/);
  const malformed = validate("0.14.0", { GITHUB_REF_NAME: "0.14.0-rc.1" });
  assert.equal(malformed.status, 1);
  assert.match(malformed.stderr, /not a plain X\.Y\.Z version/);
});
