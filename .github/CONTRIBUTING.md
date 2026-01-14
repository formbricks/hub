# Contributing to Formbricks Hub

Thank you for your interest in contributing to Formbricks Hub! We're excited to have you here. This document provides guidelines and instructions for contributing to the project.

## 🌟 Ways to Contribute

There are many ways to contribute to Formbricks Hub:

- **Report bugs** - Help us identify and fix issues
- **Suggest features** - Share ideas for new functionality
- **Improve documentation** - Fix typos, clarify instructions, add examples
- **Write code** - Fix bugs, implement features, optimize performance
- **Build connectors** - Create import scripts for new data sources
- **Community support** - Help others in GitHub Discussions

Every contribution, big or small, is valuable and appreciated!

## 🚀 Getting Started

### Prerequisites

Before you begin, ensure you have the following installed:

- **Go 1.23+** - [Download](https://go.dev/dl/)
- **PostgreSQL 18** - Via Docker (recommended) or local installation
- **Python 3.11+** - For data import scripts
- **pnpm 9+** - [Install](https://pnpm.io/installation)
- **Docker & Docker Compose** - [Get Docker](https://docs.docker.com/get-docker/)

### Local Development Setup

**1. Fork and clone the repository:**

```bash
git clone https://github.com/YOUR_USERNAME/hub.git
cd hub
```

**2. Start the infrastructure:**

```bash
# Start PostgreSQL
docker compose up -d
```

**3. Run the Hub API:**

```bash
cp env.example .env

# Edit .env with your configuration:
# - SERVICE_API_KEY=your-development-api-key
# - SERVICE_OPEN_AI_KEY=sk-... (optional, for AI enrichment)

# Run the API in development mode
make dev
```

The API will be available at `http://localhost:8888`

**4. Run the documentation site (optional):**

```bash
cd docs
pnpm install
pnpm dev
```

The docs will be available at `http://localhost:3000`

### Verify Your Setup

```bash
# Test the API
curl http://localhost:8888/health

# Run Go tests
make test

# Run linter
make lint
```

## 📝 Code Standards

We maintain high code quality standards to ensure the codebase remains maintainable and consistent.

### Go Code (Hub API)

- **Format**: Use `gofmt` (or `make fmt`)
- **Linter**: Use `golangci-lint` (or `make lint`)
- **Testing**: Write tests for new features and bug fixes
- **Error handling**: Always handle errors explicitly
- **Comments**: Document exported functions and types

```bash
# Format code
make fmt

# Run linter
make lint

# Run tests
make test

# Run tests with coverage
make test-coverage
```

### TypeScript/JavaScript (Documentation)

- **Format**: Prettier (automatically run on commit)
- **Lint**: ESLint (inside docs directory)
- **Style**: Follow existing patterns in the codebase

```bash
# In the docs directory
cd docs

# Run linter
pnpm lint
```

### Python (Import Scripts)

- **Format**: Use `black` for formatting
- **Linter**: Use `flake8` or `ruff`
- **Type hints**: Add type annotations where appropriate
- **Documentation**: Include docstrings for functions

## 🔀 Git Workflow

### Commit Messages

We use **Conventional Commits** for clear and structured commit history. This is enforced by `commitlint` (pre-commit hook coming soon).

**Format**: `type(scope): description`

**Types:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes (formatting, no logic change)
- `refactor`: Code refactoring
- `perf`: Performance improvements
- `test`: Adding or updating tests
- `chore`: Maintenance tasks, dependencies

**Examples:**

```bash
git commit -m "feat(api): add batch import endpoint"
git commit -m "fix(enrichment): handle OpenAI timeout errors"
git commit -m "docs(api): update authentication guide"
git commit -m "chore(deps): update go dependencies"
```

### Branch Naming

Use descriptive branch names with prefixes:

- `feat/add-batch-import` - New features
- `fix/enrichment-timeout` - Bug fixes
- `docs/api-examples` - Documentation updates
- `refactor/worker-pool` - Code refactoring

### Pull Request Process

1. **Fork the repository** and create your branch from `main`

2. **Make your changes** following the code standards

3. **Write or update tests** for your changes

4. **Run tests and linters locally:**
   ```bash
   make test
   make lint
   ```

5. **Commit your changes** using Conventional Commits

6. **Push to your fork** and submit a pull request

7. **Fill out the PR template** with:
   - Clear description of changes
   - Related issue number (if applicable)
   - Type of change (bug fix, feature, etc.)
   - Testing approach

8. **Wait for review** - A maintainer will review your PR
   - Address any feedback or requested changes
   - Keep the PR up to date with `main` if needed

9. **Celebrate!** 🎉 Once merged, you're an official contributor!

### PR Checklist

Before submitting your PR, ensure:

- [ ] Code follows project style guidelines
- [ ] Tests pass locally (`make test`)
- [ ] Linter passes (`make lint`)
- [ ] Documentation is updated (if needed)
- [ ] Commit messages follow Conventional Commits
- [ ] No breaking changes (or clearly documented)

## 🧪 Testing Requirements

### Go Tests

All new features and bug fixes must include tests:

- **Unit tests**: Test individual functions and methods
- **Integration tests**: Test API endpoints with real database
- **Table-driven tests**: Use table-driven tests for multiple scenarios

```go
func TestCreateExperience(t *testing.T) {
    tests := []struct {
        name    string
        input   CreateExperienceInput
        wantErr bool
    }{
        // test cases
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test logic
        })
    }
}
```

Run tests:
```bash
make test
```

### Documentation Tests

Ensure documentation builds without errors:

```bash
cd docs
pnpm build
```

## 📋 Issue Guidelines

### Reporting Bugs

When reporting a bug, please use the bug report template and include:

- **Formbricks Hub version**: e.g., v0.1.0
- **Deployment method**: Docker, Kubernetes, binary
- **Operating system**: Linux, macOS, Windows
- **Steps to reproduce**: Clear, numbered steps
- **Expected behavior**: What should happen
- **Actual behavior**: What actually happens
- **Logs**: Relevant error messages or logs
- **Screenshots**: If applicable

### Requesting Features

When requesting a feature, please use the feature request template and include:

- **Problem description**: What problem does this solve?
- **Proposed solution**: How would you like it to work?
- **Alternatives**: Any alternative solutions considered?
- **Additional context**: Mockups, examples, related issues
- **Willingness to contribute**: Can you help implement this?

### Good First Issues

Look for issues labeled `good first issue` - these are specifically chosen for newcomers and come with additional guidance.

## 🏗️ Building Connectors

Want to add support for a new data source? Here's how:

1. **Create a new directory** in `scripts/data-imports/your-source/`

2. **Add import script** (`import.py`) that:
   - Fetches data from the external API
   - Maps data to Hub's field types
   - Calls Hub's REST API

3. **Include documentation**:
   - `README.md` - Setup and usage instructions
   - `requirements.txt` - Python dependencies
   - `.env.example` - Required environment variables

4. **Test thoroughly**:
   - Test with real data
   - Test pagination (if applicable)
   - Test error handling

5. **Submit a PR** with:
   - Import script
   - Documentation
   - Example SQL queries

See existing connectors in `scripts/data-imports/` for examples.

## 🤝 Code of Conduct

This project adheres to the Contributor Covenant Code of Conduct. By participating, you are expected to uphold this code. Please report unacceptable behavior to security@formbricks.com.

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for details.

## 📦 Release Process

Releases are managed by maintainers via GitHub Releases:

1. Create a new release with a semantic version tag (e.g., `v0.2.0`)
2. GitHub Actions automatically builds and pushes Docker images to `ghcr.io`
3. Release notes are generated from commit messages
4. Docker images are tagged with version numbers

Contributors don't need to worry about releases - just focus on great code!

## 💬 Questions?

- **GitHub Discussions**: Ask questions, share ideas, get help
- **Issues**: Technical problems and feature requests
- **Email**: security@formbricks.com (security issues only)

## 🙏 Thank You!

Thank you for contributing to Formbricks Hub! Your efforts help make experience management accessible to everyone.

Every contribution counts, and we appreciate your time and expertise. ❤️

