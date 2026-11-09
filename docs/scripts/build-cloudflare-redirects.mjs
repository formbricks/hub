#!/usr/bin/env node
// Keep Cloudflare's HTTP redirects in sync with the legacy URL inventory.
import { readFileSync, writeFileSync } from "node:fs";

const redirects = JSON.parse(readFileSync("legacy-redirects.json", "utf8"));
const rules = Object.entries(redirects).flatMap(([source, destination]) => [
  `${source} ${destination} 301`,
  `${source}/ ${destination} 301`,
]);

writeFileSync("dist/_redirects", `${rules.join("\n")}\n`);
