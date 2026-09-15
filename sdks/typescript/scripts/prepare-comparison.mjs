#!/usr/bin/env node

/**
 * Packs this package, fetches the one currently on npm, extracts both, and
 * prints what the comparison scripts need.
 *
 * Shared by publish-sdk.yml (which compares packed contents to decide whether a
 * release publishes) and sdk-preview.yml (which compares the public surface for
 * a reviewer), so both always measure the same two things the same way. The
 * npm quirks worth knowing live here rather than twice in YAML:
 *
 *   - `npm view` exits non-zero both when a package does not exist and when the
 *     registry cannot be reached. Only the first means "nothing to compare"; a
 *     registry outage must not silently turn into a comparison against nothing,
 *     so the error is read, not just the exit status.
 *   - the two tarballs go in separate directories from the extracted trees.
 *     Extracting a tarball into the directory holding it leaves the .tgz beside
 *     the package's own files, where a content comparison counts it as shipped
 *     content.
 *
 * The local package is packed with lifecycle scripts skipped, so nothing the
 * build produced runs here, and `files` applies to both sides: what is compared
 * is what npm would actually ship.
 *
 * Packing reads ./package.json, so this runs from the package directory.
 *
 * Prints a JSON object on stdout: `publishedVersion` is null when the package is
 * not on npm yet, and `integrity`/`shasum` identify the packed local tarball.
 *
 * Exits 0 on success, 1 when the registry cannot be read, and 2 on a usage or
 * local packing error.
 *
 * Usage: node prepare-comparison.mjs <workdir>
 */

import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, rmSync } from "node:fs";
import path from "node:path";

const [, , workdirArgument] = process.argv;

if (!workdirArgument) {
  console.error("usage: prepare-comparison.mjs <workdir>");
  process.exit(2);
}

const workdir = path.resolve(workdirArgument);
const publishedDir = path.join(workdir, "published");
const generatedDir = path.join(workdir, "generated");
// Separate per side: the two tarballs share a filename whenever the version has
// not been bumped, and the second pack would overwrite the first.
const localTarballDir = path.join(workdir, "tarballs", "local");
const publishedTarballDir = path.join(workdir, "tarballs", "published");

function fail(code, message) {
  console.error(message);
  process.exit(code);
}

function npm(argv, { allowFailure = false } = {}) {
  try {
    const stdout = execFileSync("npm", argv, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    return { status: 0, stdout, stderr: "" };
  } catch (error) {
    if (!allowFailure) {
      fail(
        2,
        `npm ${argv.join(" ")} failed:\n${error.stderr || error.message}`,
      );
    }
    return {
      status: error.status ?? 1,
      stdout: error.stdout ?? "",
      stderr: error.stderr ?? "",
    };
  }
}

function packed(argv) {
  const { stdout } = npm([...argv, "--json"]);
  try {
    const [entry] = JSON.parse(stdout);
    if (!entry?.filename) throw new Error("no filename in npm pack output");
    return entry;
  } catch (error) {
    fail(2, `Could not read npm pack output: ${error.message}\n${stdout}`);
  }
}

function extract(tarball, into) {
  execFileSync("tar", ["-xzf", tarball, "-C", into, "--strip-components=1"], {
    stdio: ["ignore", "inherit", "inherit"],
  });
}

// Start from a clean tree so nothing from an earlier run is counted as content.
rmSync(workdir, { recursive: true, force: true });
for (const dir of [
  publishedDir,
  generatedDir,
  localTarballDir,
  publishedTarballDir,
]) {
  mkdirSync(dir, { recursive: true });
}

let name;
try {
  ({ name } = JSON.parse(readFileSync("package.json", "utf8")));
} catch (error) {
  fail(2, `Could not read ./package.json: ${error.message}`);
}
if (!name) fail(2, "./package.json has no name — run this from the package.");

const local = packed([
  "pack",
  "--ignore-scripts",
  "--pack-destination",
  localTarballDir,
]);
extract(path.join(localTarballDir, local.filename), generatedDir);

const view = npm(["view", name, "version"], { allowFailure: true });

let publishedVersion = null;
if (view.status === 0) {
  publishedVersion = view.stdout.trim();
  const remote = packed([
    "pack",
    `${name}@${publishedVersion}`,
    "--pack-destination",
    publishedTarballDir,
  ]);
  extract(path.join(publishedTarballDir, remote.filename), publishedDir);
} else if (!/E404/.test(view.stderr)) {
  fail(
    1,
    `Could not read ${name} from the npm registry — refusing to compare against nothing.\n${view.stderr
      .trim()
      .split("\n")
      .slice(-3)
      .join("\n")}`,
  );
}

console.log(
  JSON.stringify(
    {
      name,
      publishedVersion,
      published: publishedVersion === null ? null : publishedDir,
      generated: generatedDir,
      tarball: path.join(localTarballDir, local.filename),
      integrity: local.integrity,
      shasum: local.shasum,
      files: local.files?.length ?? null,
    },
    null,
    2,
  ),
);
