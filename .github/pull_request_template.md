<!-- Title: Conventional Commits. -->

<!-- `Fixes` closes the ticket on merge; use `Ref` for partial work. No ticket: delete the line, say why. -->

Fixes ENG-

## What & why

**Was:** _how it behaved before, one sentence_

**Now:** _how it behaves after this PR, one sentence_

<!-- The smallest visual that makes the point: a diff sketch, a call or file tree, or mermaid. -->

## Where to look

<!-- 1–3 permalinks pinned to the head commit, ≤10 words each. Nothing risky? Say so. -->

-

## Merge danger

- [ ] This PR contains breaking changes

Why: _one line on why it is, or is not, breaking_

**Door** (tick one)

- [ ] Two-way door: a revert undoes it
- [ ] One-way door: a revert does not undo it

**Blast radius:** _what nearby could break_

**Migrations & env:** none

<!-- Schema change: add a goose migration in `migrations/` and run `make migrate-validate`. API change: update `openapi.yaml` and run the contract tests. -->

## Evidence

Rerun: `make tests`

**Coverage**

| Behaviour | How | Outcome |
| --- | --- | --- |
|  |  |  |

<!-- UI change: before/after screenshots here. No visual change: write "No UI change". -->

**Open gaps**

- none
