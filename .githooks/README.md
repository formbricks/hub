# Git Hooks

This directory contains version-controlled git hooks for the repository.

## Installation

To install the hooks, run:

```bash
make install-hooks
```

Or manually:

```bash
cp .githooks/pre-commit .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
```

## Hooks

### pre-commit

The pre-commit hook automatically:
1. Validates migration files with `goose` (when any file under `migrations/` is staged)
2. Runs `golangci-lint run --fix` (format + fixable issues; gofumpt/gci from config)
3. Stages any auto-fixed files
4. Runs the linter to verify no remaining issues (includes gosec for secrets/hardcoded credentials in Go code)
5. Runs the fast unit tests (`make test-unit`; no database)

Steps 2–5 are skipped when the commit stages no Go-relevant files (`*.go`,
`go.mod`, `go.sum`, `Makefile`, `.golangci.yml`) — a docs-only change does not
need the Go toolchain, matching the path filters on `tests.yml` and
`code-quality.yml`. `golangci-lint` is likewise only required when those steps
actually run. Docs changes are validated by `pnpm check` / `pnpm build` from
`docs/`, enforced in CI by `docs-build.yml`.

If any step fails, the commit is blocked.

## Requirements

The hooks require:
- `golangci-lint` - Install with: `make install-tools`
- `goose` - Install with: `make install-tools` (only needed when committing changes under `migrations/`)

These are automatically installed when you run `make dev-setup`.
