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
1. Checks for potential secrets in staged changes (warning only)
2. Validates migration files with `goose` (when any file under `migrations/` is staged)
3. Formats code with `gofumpt`
4. Stages any auto-formatted files
5. Runs the linter (`golangci-lint`)

If any step fails, the commit is blocked.

## Requirements

The hooks require:
- `gofumpt` - Install with: `make install-tools`
- `golangci-lint` - Install with: `make install-tools`
- `goose` - Install with: `make install-tools` (only needed when committing changes under `migrations/`)

These are automatically installed when you run `make dev-setup`.
