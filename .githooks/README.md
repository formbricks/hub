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
2. Formats code with `gofumpt`
3. Stages any auto-formatted files
4. Runs the linter (`golangci-lint`)

If any step fails, the commit is blocked.

## Requirements

The hooks require:
- `gofumpt` - Install with: `make install-tools`
- `golangci-lint` - Install with: `make install-tools`

These are automatically installed when you run `make dev-setup`.
