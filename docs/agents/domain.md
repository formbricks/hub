# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`GLOSSARY.md`** at the repo root.
- **`docs/adr/`**: read ADRs that touch the area you're about to work in. The folder does not exist until the first ADR is recorded.

This is a single-context repo: one glossary and one ADR folder for the whole repo, with terms grouped under headings by area. ADR files are numbered in sequence (`NNNN-short-title.md`); take the next free number. The `/domain-modeling` skill adds terms and ADRs as they get resolved.

`docs/adr/` sits beside the Astro documentation site in `docs/`, outside `docs/src/`, so the site does not publish it; ADRs are engineering records, not product docs.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `GLOSSARY.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0002 (records are tenant-scoped on every path), but worth reopening because…_
