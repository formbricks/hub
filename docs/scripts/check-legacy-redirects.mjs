#!/usr/bin/env node
// Verify that every old Stainless API URL has a built redirect and a real target.
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const dist = process.argv[2] ?? "dist";
const redirects = JSON.parse(readFileSync("legacy-redirects.json", "utf8"));
const entries = Object.entries(redirects);
const errors = [];

if (entries.length < 31) {
  errors.push(
    `Expected at least the 31 known Stainless routes; found ${entries.length}.`,
  );
}

for (const [source, destination] of entries) {
  if (
    !/^\/api\/resources\/[a-z0-9_/-]+$/.test(source) ||
    !/^\/api\/operations\/[a-z0-9-]+(?:\/[a-z0-9-]+)*\/$/.test(destination)
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
