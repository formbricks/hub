#!/usr/bin/env node

/**
 * Compares the public surface of two packaged SDK directories for the
 * sdk-preview workflow.
 *
 * The surface is derived from what the package actually ships, not from a grep
 * over the source tree:
 *   - `package.json.exports` is compared subpath by subpath;
 *   - for every entry point, the exported names are read from the declaration
 *     files (`.d.mts` / `.d.ts` / `.d.cts`) and runtime files (`.mjs` / `.cjs` /
 *     `.js`) listed there, using the TypeScript compiler's module-exports API —
 *     so `export class`, default exports, `export { ... }`, `export *` and
 *     type-only exports are all counted, and internal declarations the entry
 *     point does not re-export are not.
 *
 * It compares exported *names* and the export map, not signatures: a changed
 * parameter list on an unchanged name does not show up here.
 *
 * Prints a Markdown summary to stdout. A breaking surface is for the reviewer to
 * see, not for the job to turn red, so it exits 2 — failing the job loudly —
 * only when an input cannot be read at all: a missing or invalid manifest on
 * either side, or a *generated* exports map naming files the package does not
 * ship, which is a packaging bug the PR can fix. The same fault on the npm side
 * is reported as a note and its readable entry points still compared, since
 * nothing in the PR can change a package that is already published.
 *
 * Usage: node surface.mjs <published-dir> <generated-dir> <published-version>
 */

import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import ts from "typescript";

const [, , publishedDir, generatedDir, publishedVersion] = process.argv;

if (!publishedDir || !generatedDir || !publishedVersion) {
  console.error(
    "usage: surface.mjs <published-dir> <generated-dir> <published-version>",
  );
  process.exit(2);
}

const DECLARATION_FILE = /\.d\.(?:mts|cts|ts)$/;
const RUNTIME_FILE = /\.(?:mjs|cjs|js)$/;

const compilerOptions = {
  target: ts.ScriptTarget.ESNext,
  module: ts.ModuleKind.NodeNext,
  moduleResolution: ts.ModuleResolutionKind.NodeNext,
  allowJs: true,
  checkJs: false,
  skipLibCheck: true,
  noEmit: true,
  noLib: true,
  types: [],
};

function readManifest(dir) {
  const manifestPath = path.join(dir, "package.json");
  if (!existsSync(manifestPath)) {
    console.error(`${dir} has no package.json — nothing to compare.`);
    process.exit(2);
  }

  try {
    return JSON.parse(readFileSync(manifestPath, "utf8"));
  } catch (error) {
    console.error(`${manifestPath} is not valid JSON: ${error.message}`);
    process.exit(2);
  }
}

function entryFiles(manifest) {
  const entries = new Map();

  function visit(subpath, target) {
    if (typeof target === "string") {
      // A wildcard subpath (`./core/*`) names a family of files rather than
      // one: its members are whatever the package ships, so there is no single
      // entry point here to read exports from.
      if (target.includes("*")) return;

      if (DECLARATION_FILE.test(target) || RUNTIME_FILE.test(target)) {
        if (!entries.has(subpath)) entries.set(subpath, []);
        entries.get(subpath).push(target.replace(/^\.\//, ""));
      }
      return;
    }

    if (Array.isArray(target)) {
      for (const item of target) visit(subpath, item);
      return;
    }

    if (target !== null && typeof target === "object") {
      for (const item of Object.values(target)) visit(subpath, item);
    }
  }

  const { exports } = manifest;
  if (
    exports !== null &&
    typeof exports === "object" &&
    !Array.isArray(exports)
  ) {
    for (const [subpath, target] of Object.entries(exports)) {
      visit(subpath, target);
    }
  } else if (typeof exports === "string") {
    visit(".", exports);
  } else {
    // Nothing to read the entry points from, so fall back to the legacy
    // fields. Without this, a package that predates `exports` would look like
    // it exported nothing at all.
    for (const field of ["types", "main", "module"]) {
      if (typeof manifest[field] === "string") visit(".", manifest[field]);
    }
  }

  return entries;
}

const warnings = [];

function buildSurface(dir, { label, fatalOnMissingTargets }) {
  const manifest = readManifest(dir);
  const entries = entryFiles(manifest);
  const files = [...new Set([...entries.values()].flat())];

  const missing = files.filter((file) => !existsSync(path.join(dir, file)));
  if (missing.length > 0) {
    const detail = `the exports map points at files the package does not contain: ${missing.join(", ")}`;
    // The generated package is ours, so this is a packaging bug this PR can fix
    // and the job should say so. The published one is already on npm and nothing
    // in this PR can change it — failing every preview over it would be noise,
    // so its surface is reported from the entry points that can be read. The
    // program below simply finds no source file for a missing one.
    if (fatalOnMissingTargets) {
      console.error(`${label}: ${detail}`);
      process.exit(2);
    }
    warnings.push(`${label}: ${detail}`);
  }

  const program = ts.createProgram(
    files.map((file) => path.join(dir, file)),
    compilerOptions,
  );
  const checker = program.getTypeChecker();

  const surface = new Map();
  for (const [subpath, entryFilesForSubpath] of entries) {
    const runtime = new Set();
    const types = new Set();

    for (const relativePath of entryFilesForSubpath) {
      const absolutePath = path.join(dir, relativePath);
      const sourceFile = program.getSourceFile(absolutePath);
      if (!sourceFile) continue;

      const symbol = checker.getSymbolAtLocation(sourceFile);
      if (!symbol) continue;

      for (const exported of checker.getExportsOfModule(symbol)) {
        const name = exported.getName();
        // Interop marker emitted by transpiled CommonJS, not part of the API.
        if (name === "__esModule") continue;
        (DECLARATION_FILE.test(relativePath) ? types : runtime).add(name);
      }
    }

    surface.set(subpath, {
      runtime: [...runtime].sort(),
      types: [...types].sort(),
    });
  }

  return { exports: manifest.exports ?? {}, surface };
}

function canonicalJson(value) {
  return JSON.stringify(value, null, 2);
}

function sortedKeys(object) {
  if (object === null || typeof object !== "object" || Array.isArray(object)) {
    return [];
  }
  return Object.keys(object).sort();
}

// Long lists are what these reports are made of, so they are truncated to the
// first 30 entries with a count of the rest.
function listMarkdown(items) {
  if (items.length === 0) return "—";
  const shown = items
    .slice(0, 30)
    .map((item) => `\`${item}\``)
    .join(", ");
  if (items.length <= 30) return shown;
  return `${shown}, … and ${items.length - 30} more`;
}

function namesBlock(label, names) {
  if (names.length === 0) return "";
  let block = `- ${label}: ${listMarkdown(names)}\n`;
  if (names.length > 30) {
    block += `<details><summary>All ${names.length} ${label.toLowerCase()} names</summary>\n\n\`\`\`\n${names.join("\n")}\n\`\`\`\n</details>\n`;
  }
  return block;
}

function exportMapDiff(publishedExports, generatedExports) {
  const publishedKeys = sortedKeys(publishedExports);
  const generatedKeys = sortedKeys(generatedExports);
  const publishedSet = new Set(publishedKeys);
  const generatedSet = new Set(generatedKeys);

  const removed = publishedKeys.filter((key) => !generatedSet.has(key));
  const added = generatedKeys.filter((key) => !publishedSet.has(key));
  const changed = publishedKeys.filter(
    (key) =>
      generatedSet.has(key) &&
      canonicalJson(publishedExports[key]) !==
        canonicalJson(generatedExports[key]),
  );

  return { removed, added, changed };
}

const published = buildSurface(publishedDir, {
  label: `The package on npm (\`@formbricks/hub@${publishedVersion}\`)`,
  fatalOnMissingTargets: false,
});
const generated = buildSurface(generatedDir, {
  label: "The generated package",
  fatalOnMissingTargets: true,
});
const exportDiff = exportMapDiff(published.exports, generated.exports);

// Name-level changes are only meaningful for subpaths both packages define: a
// subpath only one of them has is reported by the export-map section instead.
const comparableSubpaths = [...published.surface.keys()]
  .filter((subpath) => generated.surface.has(subpath))
  .sort();

const lines = [];

lines.push(`## SDK surface vs \`@formbricks/hub@${publishedVersion}\``);
lines.push("");

if (warnings.length > 0) {
  for (const warning of warnings) lines.push(`> [!NOTE]`, `> ${warning}`, "");
}

lines.push("### Export map");
lines.push("");
lines.push(`- Removed subpaths: ${listMarkdown(exportDiff.removed)}`);
lines.push(`- Added subpaths: ${listMarkdown(exportDiff.added)}`);
lines.push(`- Changed subpaths: ${listMarkdown(exportDiff.changed)}`);
lines.push("");

if (
  exportDiff.removed.length > 0 ||
  exportDiff.added.length > 0 ||
  exportDiff.changed.length > 0
) {
  lines.push("<details>");
  lines.push("<summary>Full export maps</summary>");
  lines.push("");
  lines.push("#### Published");
  lines.push("```json");
  lines.push(canonicalJson(published.exports));
  lines.push("```");
  lines.push("#### Generated");
  lines.push("```json");
  lines.push(canonicalJson(generated.exports));
  lines.push("```");
  lines.push("</details>");
  lines.push("");
}

const removedNames = [];
const addedNames = [];

for (const subpath of comparableSubpaths) {
  const publishedSurface = published.surface.get(subpath);
  const generatedSurface = generated.surface.get(subpath);

  for (const kind of ["runtime", "types"]) {
    const before = publishedSurface[kind];
    const after = generatedSurface[kind];
    const removed = before.filter((name) => !after.includes(name));
    const added = after.filter((name) => !before.includes(name));
    if (removed.length > 0) {
      removedNames.push({ subpath, kind, names: removed });
    }
    if (added.length > 0) {
      addedNames.push({ subpath, kind, names: added });
    }
  }
}

lines.push("### Removed or renamed — breaking for consumers");
lines.push("");
if (exportDiff.removed.length > 0) {
  lines.push(`- Removed entry points: ${listMarkdown(exportDiff.removed)}`);
}
if (removedNames.length === 0) {
  lines.push(
    "- No exported names removed from the entry points present in both packages.",
  );
} else {
  for (const { subpath, kind, names } of removedNames) {
    lines.push(namesBlock(`\`${subpath}\` (${kind})`, names).trimEnd());
  }
}
lines.push("");

lines.push("### Added");
lines.push("");
if (exportDiff.added.length > 0) {
  lines.push(`- Added entry points: ${listMarkdown(exportDiff.added)}`);
}
if (addedNames.length === 0) {
  lines.push("- No exported names added.");
} else {
  for (const { subpath, kind, names } of addedNames) {
    lines.push(namesBlock(`\`${subpath}\` (${kind})`, names).trimEnd());
  }
}
lines.push("");

lines.push(
  "If anything is listed as removed, bump the version in `sdks/typescript/package.json` accordingly.",
);

console.log(lines.join("\n"));
