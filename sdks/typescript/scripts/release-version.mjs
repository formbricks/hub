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
 *   check <version> [<latest on npm>]
 *     Fails unless <version> is strictly above the version npm calls latest.
 *     npm would refuse the publish anyway (it will not move `latest`
 *     backwards implicitly), but only after the release has been approved;
 *     this fails the build job instead, with a message that says which Hub
 *     version would work. No latest on npm passes.
 *
 * Exits 0 on success, 1 when the version is refused, and 2 on usage errors.
 */

import { readFileSync, realpathSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const RELEASE = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

/** The version a release tag names, or undefined when it is not a plain X.Y.Z. */
export function versionFromTag(tag) {
  const match = RELEASE.exec(tag ?? "");
  return match ? `${match[1]}.${match[2]}.${match[3]}` : undefined;
}

/**
 * Compares two X.Y.Z versions numerically: negative, zero or positive. Only
 * plain releases are compared, which is all `latest` ever names here.
 */
export function compareVersions(a, b) {
  const left = a.split(".").map(Number);
  const right = b.split(".").map(Number);
  for (let index = 0; index < 3; index++) {
    if (left[index] !== right[index]) return left[index] - right[index];
  }
  return 0;
}

/** Why <version> cannot be published over <latest>, or undefined when it can. */
export function refusal(version, latest) {
  if (!latest) return undefined;
  if (!versionFromTag(latest)) {
    return `npm's latest is ${latest}, which is not a plain release; refusing to compare against it.`;
  }
  const order = compareVersions(version, latest);
  if (order > 0) return undefined;
  if (order === 0) {
    return `@formbricks/hub@${version} is already npm's latest. A Hub release publishes its own SDK version, so this one cannot be published twice.`;
  }
  return `Hub ${version} is below @formbricks/hub@${latest} on npm, and npm will not move latest backwards. The SDK takes its version from the Hub release, so release Hub ${nextMinor(latest)} or later.`;
}

function nextMinor(version) {
  const [major, minor] = version.split(".").map(Number);
  return `${major}.${minor + 1}.0`;
}

function main(argv, env) {
  const [command, ...args] = argv;
  const manifestPath = path.join(
    path.dirname(fileURLToPath(import.meta.url)),
    "..",
    "package.json",
  );

  if (command === "stamp") {
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

  if (command === "check" && args.length >= 1) {
    const [version, latest] = args;
    if (!versionFromTag(version)) {
      console.error(`${JSON.stringify(version)} is not a plain X.Y.Z version.`);
      return 1;
    }
    const reason = refusal(version, latest);
    if (reason) {
      console.error(reason);
      return 1;
    }
    return 0;
  }

  console.error(
    "usage: release-version.mjs stamp | check <version> [<latest on npm>]",
  );
  return 2;
}

// Compared as real paths: run through a symlinked directory (macOS's /var is
// /private/var), argv[1] and the module URL name the same file differently.
if (
  process.argv[1] &&
  realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url))
) {
  process.exit(main(process.argv.slice(2), process.env));
}
