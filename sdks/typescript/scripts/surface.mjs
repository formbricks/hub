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
  // subpath -> condition path ("import.default", "require.types", …) -> file.
  //
  // The conditions are kept apart rather than flattened into one set per
  // subpath. A consumer only ever resolves one of them, so unioning their
  // exports hides the case where a name disappears from `import` but survives
  // in `require`: the union still contains it, and a change that breaks every
  // ESM consumer reports as no change at all.
  const entries = new Map();

  function visit(subpath, target, condition) {
    if (typeof target === "string") {
      // A wildcard subpath (`./core/*`) names a family of files rather than
      // one: its members are whatever the package ships, so there is no single
      // entry point here to read exports from.
      if (target.includes("*")) return;

      if (DECLARATION_FILE.test(target) || RUNTIME_FILE.test(target)) {
        if (!entries.has(subpath)) entries.set(subpath, new Map());
        entries.get(subpath).set(condition, target.replace(/^\.\//, ""));
      }
      return;
    }

    if (Array.isArray(target)) {
      // A fallback array: each element is a separate resolution candidate.
      target.forEach((item, index) =>
        visit(subpath, item, `${condition}[${index}]`),
      );
      return;
    }

    if (target !== null && typeof target === "object") {
      for (const [key, item] of Object.entries(target)) {
        visit(subpath, item, condition ? `${condition}.${key}` : key);
      }
    }
  }

  const { exports } = manifest;
  if (
    exports !== null &&
    typeof exports === "object" &&
    !Array.isArray(exports)
  ) {
    for (const [subpath, target] of Object.entries(exports)) {
      visit(subpath, target, "");
    }
  } else if (typeof exports === "string") {
    visit(".", exports, "default");
  } else {
    // Nothing to read the entry points from, so fall back to the legacy
    // fields. Without this, a package that predates `exports` would look like
    // it exported nothing at all.
    for (const field of ["types", "main", "module"]) {
      if (typeof manifest[field] === "string") {
        visit(".", manifest[field], field);
      }
    }
  }

  return entries;
}

const warnings = [];

function buildSurface(dir, { label, fatalOnMissingTargets }) {
  const manifest = readManifest(dir);
  const entries = entryFiles(manifest);
  const files = [
    ...new Set(
      [...entries.values()].flatMap((conditions) => [...conditions.values()]),
    ),
  ];

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
  for (const [subpath, conditions] of entries) {
    const perCondition = new Map();

    for (const [condition, relativePath] of conditions) {
      const absolutePath = path.join(dir, relativePath);
      const sourceFile = program.getSourceFile(absolutePath);
      if (!sourceFile) continue;

      const symbol = checker.getSymbolAtLocation(sourceFile);
      if (!symbol) continue;

      const names = new Set();
      for (const exported of checker.getExportsOfModule(symbol)) {
        const name = exported.getName();
        // Interop marker emitted by transpiled CommonJS, not part of the API.
        if (name === "__esModule") continue;
        names.add(name);
      }

      perCondition.set(condition, {
        kind: DECLARATION_FILE.test(relativePath) ? "types" : "runtime",
        names: [...names].sort(),
      });
    }

    surface.set(subpath, perCondition);
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

function label(subpath, conditions) {
  return `\`${subpath}\` via ${conditions.map((c) => `\`${c}\``).join(", ")}`;
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

// Conditions that report the same names are merged into one line: a package
// whose `import` and `require` builds agree — the normal case — reads as one
// entry rather than four near-identical ones, and a divergence stands out
// precisely because it fails to merge.
function collect(into, subpath, byCondition) {
  for (const [condition, names] of byCondition) {
    const existing = into.find(
      (entry) =>
        entry.subpath === subpath &&
        entry.names.length === names.length &&
        entry.names.every((name, index) => name === names[index]),
    );
    if (existing) existing.conditions.push(condition);
    else into.push({ subpath, conditions: [condition], names });
  }
}

// Conditions present on only one side cannot be name-compared: the entry moved
// rather than changed, and there is no counterpart to diff against. That is a
// real answer, but a silent one, so it is stated rather than left as an empty
// section a reviewer would read as "nothing changed here".
const unmatchedConditions = [];

for (const subpath of comparableSubpaths) {
  const publishedConditions = published.surface.get(subpath);
  const generatedConditions = generated.surface.get(subpath);

  const onlyPublished = [...publishedConditions.keys()].filter(
    (condition) => !generatedConditions.has(condition),
  );
  const onlyGenerated = [...generatedConditions.keys()].filter(
    (condition) => !publishedConditions.has(condition),
  );
  if (onlyPublished.length > 0 || onlyGenerated.length > 0) {
    unmatchedConditions.push({ subpath, onlyPublished, onlyGenerated });
  }

  const removedByCondition = new Map();
  const addedByCondition = new Map();

  for (const [condition, { names: after }] of generatedConditions) {
    // A condition only one side defines is an export-map change, reported as
    // such above; there is no like-for-like name comparison to make.
    const before = publishedConditions.get(condition)?.names;
    if (!before) continue;

    const removed = before.filter((name) => !after.includes(name));
    const added = after.filter((name) => !before.includes(name));
    if (removed.length > 0) removedByCondition.set(condition, removed);
    if (added.length > 0) addedByCondition.set(condition, added);
  }

  collect(removedNames, subpath, removedByCondition);
  collect(addedNames, subpath, addedByCondition);
}

lines.push("### Removed or renamed — breaking for consumers");
lines.push("");
for (const { subpath, onlyPublished, onlyGenerated } of unmatchedConditions) {
  const sides = [];
  if (onlyPublished.length > 0) {
    sides.push(`only on npm: ${listMarkdown(onlyPublished)}`);
  }
  if (onlyGenerated.length > 0) {
    sides.push(`only generated: ${listMarkdown(onlyGenerated)}`);
  }
  lines.push(
    `> [!NOTE]`,
    `> \`${subpath}\` resolves through different conditions in the two packages (${sides.join("; ")}), so the names under those were not compared. Read the export maps above.`,
    "",
  );
}
if (exportDiff.removed.length > 0) {
  lines.push(`- Removed entry points: ${listMarkdown(exportDiff.removed)}`);
}
if (removedNames.length === 0) {
  lines.push(
    "- No exported names removed from the entry points present in both packages.",
  );
} else {
  for (const { subpath, conditions, names } of removedNames) {
    lines.push(namesBlock(label(subpath, conditions), names).trimEnd());
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
  for (const { subpath, conditions, names } of addedNames) {
    lines.push(namesBlock(label(subpath, conditions), names).trimEnd());
  }
}
lines.push("");

lines.push(
  "If anything is listed as removed, bump the version in `sdks/typescript/package.json` accordingly.",
);

console.log(lines.join("\n"));
