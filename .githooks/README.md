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

If any step fails, the commit is blocked.

## Requirements

The hooks require:
- `golangci-lint` - Install with: `make install-tools`
- `goose` - Install with: `make install-tools` (only needed when committing changes under `migrations/`)

These are automatically installed when you run `make dev-setup`.
