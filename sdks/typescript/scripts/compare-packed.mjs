#!/usr/bin/env node

/**
 * Compares two extracted npm tarballs for the publish-sdk workflow.
 *
 * The comparison is over file contents, not tarball bytes, so packaging
 * differences (file ordering, mtimes, compression) never count as a package
 * change. The only intentional normalization is `package.json.version`: bumping
 * the version is the input to a release, not evidence that the package changed.
 * Every other file — dist, src, README, LICENSE — is compared byte for byte,
 * and the manifest field by field, so a change that touches only the exports
 * map, the peer range or the documentation still counts as a change.
 *
 * Prints one line per differing path, prefixed with A (added), D (deleted) or
 * M (modified); a modified manifest also names the fields that differ, since
 * "M package.json" alone does not say what changed.
 *
 * Exits 0 when the packages are identical, 1 when they differ, and 2 when the
 * arguments do not point at two extracted packages.
 *
 * Usage: node compare-packed.mjs <published-dir> <generated-dir>
 */

import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";

const MANIFEST = "package.json";

const [, , publishedDir, generatedDir] = process.argv;

if (!publishedDir || !generatedDir) {
  console.error("usage: compare-packed.mjs <published-dir> <generated-dir>");
  process.exit(2);
}

for (const dir of [publishedDir, generatedDir]) {
  const stats = await stat(dir).catch(() => null);
  if (!stats?.isDirectory()) {
    console.error(`${dir} is not a directory — extract both tarballs first.`);
    process.exit(2);
  }
}

async function listFiles(root) {
  // npm tarballs hold regular files and directories, and that is all either side
  // of this comparison should contain: an entry that is neither (a symlink, say)
  // is not shipped content and is deliberately not counted.
  const files = [];

  async function walk(dir) {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const absolute = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        await walk(absolute);
      } else if (entry.isFile()) {
        files.push(path.relative(root, absolute));
      }
    }
  }

  await walk(root);
  return files;
}

/**
 * The version is the release's input, not part of the package, so it is left
 * out: with an otherwise unchanged package, a bumped version must not force a
 * publish.
 */
function comparableManifest(manifest) {
  const comparable = { ...manifest };
  delete comparable.version;
  return comparable;
}

async function readComparableManifest(root) {
  const manifest = await readFile(path.join(root, MANIFEST), "utf8").then(
    JSON.parse,
  );
  return comparableManifest(manifest);
}

function differingManifestFields(published, generated) {
  const fields = new Set([
    ...Object.keys(published),
    ...Object.keys(generated),
  ]);
  return [...fields]
    .filter(
      (field) =>
        JSON.stringify(published[field]) !== JSON.stringify(generated[field]),
    )
    .sort();
}

const [publishedFiles, generatedFiles] = await Promise.all([
  listFiles(publishedDir),
  listFiles(generatedDir),
]);

const published = new Set(publishedFiles);
const generated = new Set(generatedFiles);
const changes = [];

for (const file of published) {
  if (!generated.has(file)) changes.push(`D ${file}`);
}
for (const file of generated) {
  if (!published.has(file)) changes.push(`A ${file}`);
}

for (const file of published) {
  if (!generated.has(file)) continue;

  if (file === MANIFEST) {
    const [before, after] = await Promise.all([
      readComparableManifest(publishedDir),
      readComparableManifest(generatedDir),
    ]);
    const fields = differingManifestFields(before, after);
    if (fields.length > 0) {
      changes.push(`M ${file} (fields: ${fields.join(", ")})`);
    }
    continue;
  }

  const [before, after] = await Promise.all([
    readFile(path.join(publishedDir, file)),
    readFile(path.join(generatedDir, file)),
  ]);
  if (!before.equals(after)) changes.push(`M ${file}`);
}

if (changes.length === 0) process.exit(0);

changes.sort();
for (const change of changes) console.log(change);
process.exit(1);
