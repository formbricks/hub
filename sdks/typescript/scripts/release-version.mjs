#!/usr/bin/env node

/**
 * The SDK's version is the Hub release's version: @formbricks/hub@X is the
 * client generated from Hub X's openapi.yaml. Nobody bumps package.json by hand;
 * publish-sdk.yml stamps the version from the release tag. Two commands:
 *
 *   stamp
 *     On a tag ref, reads the version from the tag (`0.14.0`, or `v0.14.0`),
 *     writes it into package.json and prints it. Anything other than a plain
 *     X.Y.Z fails: prereleases never reach this job, and a malformed tag must
 *     not become a published version. Off a tag — a dry run from a branch —
 *     package.json is left alone and its version printed.
 *
 *   plan <version> <versions.json>
 *     Decides the npm dist-tag <version> publishes under, given every version
 *     already published (what `npm view @formbricks/hub versions --json`
 *     prints), and prints it:
 *
 *       - `latest`, when <version> is above every published release. The
 *         publish then passes no --tag, which keeps npm's own refusal to move
 *         `latest` backwards.
 *       - `release-X.Y`, when it is below one: a backport, such as a 0.14.1
 *         security fix released after 0.15.0. Hub releases those as stable but
 *         not latest, and the SDK follows, so `latest` stays where it is.
 *
 *     It refuses — exit 1, saying why — a version already on npm, and a
 *     backport older than one already published on its own line, which would
 *     move that line's tag backwards. Published prereleases are ignored, as
 *     npm's own ordering ignores them.
 *
 * Exits 0 on success, 1 when the version is refused, and 2 on usage errors.
 */

import { readFileSync, realpathSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Duplicated in publish-sdk.yml's publish job, which has no checkout to load
// this from; tests/release-version.test.mjs pins the two equal.
export const RELEASE = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

/** The version a release tag names, or undefined when it is not a plain X.Y.Z. */
export function versionFromTag(tag) {
  const match = RELEASE.exec(tag ?? "");
  return match ? `${match[1]}.${match[2]}.${match[3]}` : undefined;
}

/** Whether <value> is a plain X.Y.Z version — no `v`, which only a tag carries. */
export function isPlainVersion(value) {
  return typeof value === "string" && versionFromTag(value) === value;
}

/** Compares two plain X.Y.Z versions numerically: negative, zero or positive. */
export function compareVersions(a, b) {
  const left = a.split(".").map(Number);
  const right = b.split(".").map(Number);
  for (let index = 0; index < 3; index++) {
    if (left[index] !== right[index]) return left[index] - right[index];
  }
  return 0;
}

/** The dist-tag a backport of <version> publishes under: `release-X.Y`. */
export function lineTag(version) {
  const [major, minor] = version.split(".");
  return `release-${major}.${minor}`;
}

/**
 * The dist-tag <version> publishes under, given every published version:
 * `{ tag }`, or `{ refused }` saying why it cannot publish.
 */
export function plan(version, published) {
  if (!isPlainVersion(version)) {
    return {
      refused: `${JSON.stringify(version)} is not a plain X.Y.Z version.`,
    };
  }
  if (published.includes(version)) {
    return {
      refused: `@formbricks/hub@${version} is already on npm. A Hub release publishes its own SDK version, so this one cannot be published twice.`,
    };
  }
  const above = published.filter(
    (other) => isPlainVersion(other) && compareVersions(other, version) > 0,
  );
  if (above.length === 0) return { tag: "latest" };

  const tag = lineTag(version);
  const newerOnLine = above
    .filter((other) => lineTag(other) === tag)
    .sort(compareVersions);
  if (newerOnLine.length > 0) {
    return {
      refused: `@formbricks/hub@${newerOnLine.at(-1)} is already published, so ${version} would move the ${tag} tag backwards. Release a newer patch on that line instead.`,
    };
  }
  return { tag };
}

/** The version list `npm view <pkg> versions --json` prints. */
export function parseVersions(json) {
  const text = json.trim();
  if (text === "") return [];
  let parsed = JSON.parse(text);
  // Defensive about npm's shape: npm 12 wraps some `npm view --json` results
  // in an array, and a package with a single version can print a bare string.
  if (
    Array.isArray(parsed) &&
    parsed.length === 1 &&
    Array.isArray(parsed[0])
  ) {
    parsed = parsed[0];
  }
  if (typeof parsed === "string") parsed = [parsed];
  if (
    !Array.isArray(parsed) ||
    !parsed.every((value) => typeof value === "string")
  ) {
    throw new Error("not a list of versions");
  }
  return parsed;
}

function main(argv, env) {
  const [command, ...args] = argv;
  const manifestPath = path.join(
    path.dirname(fileURLToPath(import.meta.url)),
    "..",
    "package.json",
  );

  if (command === "stamp" && args.length === 0) {
    const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
    if (env.GITHUB_REF_TYPE !== "tag") {
      console.log(manifest.version);
      return 0;
    }
    const version = versionFromTag(env.GITHUB_REF_NAME);
    if (!version) {
      console.error(
        `The release tag ${JSON.stringify(env.GITHUB_REF_NAME ?? "")} is not a plain X.Y.Z version, so it cannot be the SDK's version.`,
      );
      return 1;
    }
    manifest.version = version;
    writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
    console.log(version);
    return 0;
  }

  if (command === "plan" && args.length === 2) {
    const [version, versionsFile] = args;
    let published;
    try {
      published = parseVersions(readFileSync(versionsFile, "utf8"));
    } catch (error) {
      console.error(
        `Could not read the published versions from ${versionsFile}: ${error.message}`,
      );
      return 2;
    }
    const decision = plan(version, published);
    if (decision.refused) {
      console.error(decision.refused);
      return 1;
    }
    console.log(decision.tag);
    return 0;
  }

  console.error(
    "usage: release-version.mjs stamp | plan <version> <versions.json>",
  );
  return 2;
}

// Run as a script, not when imported. Compared as real paths, since through a
// symlinked directory (macOS's /var is /private/var) argv[1] and the module URL
// name the same file differently; an argv[1] that is no file at all means this
// module was imported, not run.
function isEntryPoint() {
  try {
    return (
      realpathSync(process.argv[1]) ===
      realpathSync(fileURLToPath(import.meta.url))
    );
  } catch {
    return false;
  }
}

if (isEntryPoint()) {
  process.exit(main(process.argv.slice(2), process.env));
}
