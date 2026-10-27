#!/usr/bin/env node
// Verify that the captured Stainless URL inventory has built redirects and real targets.
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const dist = process.argv[2] ?? "dist";
const inventory = JSON.parse(
  readFileSync("legacy-stainless-routes.json", "utf8"),
);
const redirects = JSON.parse(readFileSync("legacy-redirects.json", "utf8"));
const entries = Object.entries(redirects);
const errors = [];

if (new Set(inventory).size !== inventory.length) {
  errors.push("The legacy route inventory contains duplicate paths.");
}
for (const source of inventory) {
  if (!(source in redirects)) errors.push(`Missing redirect for ${source}`);
}
for (const [source] of entries) {
  if (!inventory.includes(source))
    errors.push(`Uninventoried redirect: ${source}`);
}

for (const [source, destination] of entries) {
  if (
    !/^\/api\/(?:resources\/[a-z0-9_/-]+|typescript)$/.test(source) ||
    !/^\/[a-z0-9_/-]+\/$/.test(destination)
  ) {
    errors.push(`Invalid redirect path: ${source} -> ${destination}`);
    continue;
  }

  const sourceFile = join(dist, source.slice(1), "index.html");
  const destinationFile = join(dist, destination.slice(1), "index.html");
  if (!existsSync(sourceFile)) {
    errors.push(`Missing redirect page: ${sourceFile}`);
    continue;
  }
  if (!existsSync(destinationFile)) {
    errors.push(`Missing destination page: ${destinationFile}`);
  }
  const html = readFileSync(sourceFile, "utf8");
  if (!html.includes(`http-equiv="refresh" content="0;url=${destination}"`)) {
    errors.push(`Wrong redirect destination in ${sourceFile}`);
  }
}

if (errors.length) {
  for (const error of errors) console.error(error);
  process.exit(1);
}

console.log(
  `Verified ${entries.length} legacy redirects and their destinations.`,
);
