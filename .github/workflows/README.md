# GitHub Workflows

Documentation for all CI/CD workflows in the Formbricks Hub repository.

## Overview

| Workflow | Type | Trigger | Purpose |
|----------|------|---------|---------|
| `ci.yml` | Automated | Push, Pull Request | Tests, linting, and formatting checks |
| `release.yml` | Automated | Release published | Builds and publishes Docker images |
| `docker-build.yml` | Reusable | Called by other workflows | Docker image build logic |
| `manual-docker-build.yml` | Manual | Workflow dispatch | Manual Docker builds for testing |

## Workflows

### `ci.yml` - Continuous Integration

**Triggers:** Push to `main`, Pull Requests to `main`

**Jobs:**
- **test-go**: Runs Go tests with PostgreSQL 18 + pgvector
- **build-docs**: Builds Docusaurus documentation site

**What it does:**
- Runs Go tests with race detection and coverage (`make test`)
- Lints Go code with golangci-lint v2.5.0
- Validates Go code formatting with gofmt
- Builds documentation to verify no broken links

**Services:**
- PostgreSQL 18 with pgvector extension enabled

**Requirements:**
- Go 1.25.3
- pnpm 9
- Node.js 20

---

### `release.yml` - Release

**Triggers:** GitHub release published

**Jobs:**
- **docker**: Calls `docker-build.yml` to build and publish Docker images

**What it does:**
- Builds multi-platform Docker images (linux/amd64, linux/arm64)
- Publishes to GitHub Container Registry
- Generates artifact attestations for supply chain security
- Tags images with semantic versions and `latest`

**Image:** `ghcr.io/formbricks/hub`

---

### `docker-build.yml` - Docker Build (Reusable)

**Type:** Reusable workflow (called by other workflows)

**Purpose:** Centralized Docker image building logic for consistency and maintainability.

**Inputs:**

| Input | Required | Default | Description |
|-------|----------|---------|-------------|
| `image-name` | Yes | - | Docker image name (e.g., `formbricks/hub`) |
| `context` | No | `.` | Build context path |
| `dockerfile` | No | `./Dockerfile` | Dockerfile path |
| `platforms` | No | `linux/amd64,linux/arm64` | Target platforms |
| `push` | No | `true` | Push image to registry |
| `tags-input` | No | - | Additional custom tags |
| `enable-attestation` | No | `true` | Generate artifact attestation |

**Outputs:**
- `image-tags`: Generated Docker image tags
- `image-digest`: Image SHA256 digest

**Features:**
- Multi-platform builds
- Semantic version tagging
- GitHub Actions cache optimization
- Build summaries in workflow output
- SLSA provenance attestation

---

### `manual-docker-build.yml` - Manual Docker Build

**Triggers:** Manual workflow dispatch

**Purpose:** Build Docker images manually for testing without creating a release.

**Inputs:**
- `tag`: Custom tag for the image (e.g., `test`, `dev`, `feature-name`)
- `platforms`: Target platforms (both, amd64 only, or arm64 only)
- `push`: Whether to push to registry (checkbox, default: false)

**Use cases:**
- Testing Docker builds before release
- Creating feature branch images for testing
- Debugging build issues
- Validating builds without pushing

---

## Docker Images

**Registry:** GitHub Container Registry (`ghcr.io`)  
**Image Name:** `ghcr.io/formbricks/hub`

### Tags

**Releases create:**
- `v1.2.3` - Full semantic version
- `v1.2` - Major.minor version
- `v1` - Major version
- `latest` - Latest stable release (non-prereleases only)
- `main-{sha}` - Git commit SHA

**Manual builds create:**
- Custom tag specified in workflow inputs

### Pulling Images

```bash
# Latest release
docker pull ghcr.io/formbricks/hub:latest

# Specific version
docker pull ghcr.io/formbricks/hub:v1.0.0

# Custom tag from manual build
docker pull ghcr.io/formbricks/hub:test
```

---

## Permissions

Workflows use the following GitHub token permissions:

- `contents: read` - Read repository code
- `packages: write` - Push to GitHub Container Registry
- `attestations: write` - Generate SLSA provenance
- `id-token: write` - OIDC token for attestations
- `pull-requests: read` - Read PR diffs for golangci-lint

---

## Running Workflows

### Continuous Integration

Runs automatically on every push and pull request. No manual action needed.

### Creating a Release

```bash
# Create and push a tag
git tag v1.0.0
git push origin v1.0.0

# Create release on GitHub
gh release create v1.0.0 --generate-notes
```

This triggers the `release.yml` workflow which builds and publishes the Docker image.

### Manual Docker Build

1. Go to **Actions** tab on GitHub
2. Select **Manual Docker Build** workflow
3. Click **Run workflow**
4. Configure:
   - **tag**: Enter custom tag (e.g., `test`)
   - **platforms**: Select target platforms
   - **push**: Check to push to registry (uncheck for local test)
5. Click **Run workflow**

---

## Troubleshooting

### CI Failures

**Tests failing with database errors:**
- Check that pgvector extension is enabled in logs
- Verify PostgreSQL service is healthy
- Ensure test database connection string is correct

**Linter errors:**
- Run locally: `make lint`
- Check that you're using Go 1.25.3: `go version`
- Verify golangci-lint configuration in `.golangci.yml`

**Format check failures:**
- Run locally: `gofmt -s -w .`
- Commit the formatted code

### Docker Build Failures

**Authentication error:**
- Verify workflow has `packages: write` permission
- Check that `GITHUB_TOKEN` is available

**Multi-platform build issues:**
- Try building single platform first to isolate issue
- Check buildx and QEMU setup in workflow logs

**Build context errors:**
- Verify Dockerfile exists at `./Dockerfile`
- Check that context path `.` is correct

---

## Modifying Workflows

### Adding a New Release Step

Edit `release.yml` and add a new job:

```yaml
my-task:
  name: My Release Task
  runs-on: ubuntu-latest
  steps:
    - name: Checkout
      uses: actions/checkout@v4
    - name: Do something
      run: echo "Release task"
```

### Changing Docker Build Configuration

Edit inputs in `release.yml` where `docker-build.yml` is called:

```yaml
with:
  platforms: linux/amd64  # Change platforms
  enable-attestation: false  # Disable attestation
```

### Updating CI Steps

Edit `ci.yml` to add new test jobs, update Go version, or modify linter configuration.

---

## References

- [Repository](https://github.com/formbricks/hub)
- [Docker Images](https://github.com/orgs/formbricks/packages/container/package/hub)
- [Releases](https://github.com/formbricks/hub/releases)
