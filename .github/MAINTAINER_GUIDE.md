# Maintainer Documentation

This document provides guidance for Formbricks Hub maintainers on repository management, release processes, and best practices.

## Repository Settings

### Branch Protection Rules

Configure the following branch protection rules for the `main` branch:

**Required:**
- ✅ Require a pull request before merging
  - Require 1 approval from code owners or maintainers
  - Dismiss stale pull request approvals when new commits are pushed
- ✅ Require status checks to pass before merging
  - Required checks:
    - `Test Go API`
    - `Build Documentation`
    - `Lint Python Scripts`
  - Require branches to be up to date before merging
- ✅ Require conversation resolution before merging
- ✅ Do not allow bypassing the above settings (even for administrators)

**Optional but Recommended:**
- ✅ Require signed commits
- ✅ Require linear history

### Repository Labels

Use these labels to categorize issues and PRs:

**Type:**
- `bug` - Something isn't working
- `enhancement` - New feature or request
- `documentation` - Improvements or additions to documentation
- `refactor` - Code refactoring
- `performance` - Performance improvements
- `security` - Security-related issues

**Component:**
- `api` - Hub API related
- `enrichment` - AI enrichment system
- `docs` - Documentation site
- `superset` - Apache Superset integration
- `import-scripts` - Data import scripts
- `webhooks` - Webhook system
- `ci` - CI/CD and automation

**Priority:**
- `critical` - Critical issue requiring immediate attention
- `high-priority` - Important issue
- `low-priority` - Nice to have

**Status:**
- `good first issue` - Good for newcomers
- `help wanted` - Extra attention is needed
- `needs-discussion` - Requires team discussion
- `needs-investigation` - Requires further investigation
- `breaking-change` - Introduces breaking changes
- `wontfix` - This will not be worked on

**Dependencies:**
- `dependencies` - Pull requests that update dependencies
- `go` - Go dependencies
- `github-actions` - GitHub Actions updates
- `docker` - Docker image updates

### Repository Topics

Ensure these topics are set on GitHub for discoverability:

- `experience-management`
- `feedback`
- `analytics`
- `golang`
- `postgresql`
- `ai-enrichment`
- `customer-feedback`
- `sentiment-analysis`
- `apache-superset`
- `survey-data`
- `nps`
- `csat`
- `open-source`

### GitHub Discussions

Enable GitHub Discussions with these categories:

- 💡 **Ideas** - Share ideas for new features
- 🙏 **Q&A** - Ask the community for help
- 🙌 **Show and Tell** - Share what you've built with Hub
- 📣 **Announcements** - Updates from maintainers
- 🐛 **Bug Reports** - Community bug reports (redirect to Issues)

## Release Process

Formbricks Hub follows semantic versioning (semver) and uses GitHub Releases.

### Version Numbers

- **Major** (x.0.0): Breaking changes
- **Minor** (0.x.0): New features, backwards compatible
- **Patch** (0.0.x): Bug fixes, backwards compatible

### Creating a Release

1. **Prepare the release:**
   ```bash
   # Ensure main branch is up to date
   git checkout main
   git pull origin main
   
   # Ensure all tests pass
   make test
   cd docs && pnpm build
   ```

2. **Create and push a version tag:**
   ```bash
   # Create annotated tag
   git tag -a v0.2.0 -m "Release v0.2.0"
   
   # Push tag to trigger release workflow
   git push origin v0.2.0
   ```

3. **Create GitHub Release:**
   - Go to: https://github.com/formbricks/hub/releases/new
   - Select the tag you just created
   - Release title: `v0.2.0` (same as tag)
   - Generate release notes automatically (or write custom notes)
   - Categorize changes:
     - 🚀 Features
     - 🐛 Bug Fixes
     - 📝 Documentation
     - ⚙️ Chores
   - Publish release

4. **Automated actions:**
   - GitHub Actions will automatically:
     - Build Docker images
     - Push to `ghcr.io/formbricks/hub:v0.2.0`
     - Tag as `ghcr.io/formbricks/hub:latest` (for latest release)
     - Generate build provenance attestation

5. **Post-release verification:**
   ```bash
   # Pull and test the Docker image
   docker pull ghcr.io/formbricks/hub:v0.2.0
   docker run --rm ghcr.io/formbricks/hub:v0.2.0 --version
   ```

6. **Announce the release:**
   - GitHub Discussions (Announcements category)
   - Update documentation if needed

### Hotfix Releases

For critical bug fixes:

1. Create a hotfix branch from the release tag
2. Apply the fix
3. Create a new patch version tag (e.g., v0.2.1)
4. Follow the normal release process

## Code Review Guidelines

When reviewing pull requests:

### Checklist

- [ ] Code follows project style guidelines (gofmt, prettier)
- [ ] Commits follow conventional commits format
- [ ] Tests are included and passing
- [ ] Documentation is updated (if needed)
- [ ] No sensitive information (API keys, passwords) in code
- [ ] Breaking changes are clearly documented
- [ ] Performance implications are considered
- [ ] Security implications are reviewed

### Review Focus Areas

**For Go Code:**
- Error handling is explicit and appropriate
- Database queries are efficient (check for N+1 queries)
- Concurrency is handled safely (proper mutex usage, channel patterns)
- Context is passed correctly for cancellation
- OpenAPI documentation is updated

**For Documentation:**
- Examples are accurate and tested
- Links are valid and not broken
- Screenshots are up to date
- Installation instructions work

**For Data Import Scripts:**
- Error handling for API failures
- Pagination is implemented correctly
- Rate limiting is respected
- Credentials are loaded from environment variables

### Merge Strategy

Use **Squash and Merge** for most PRs to maintain a clean git history:
- Ensure the commit message follows conventional commits
- Include PR number in the commit message
- Single commit per PR in main branch

Use **Rebase and Merge** for:
- Multiple related commits that should be preserved
- Coordinated work across multiple contributors

## Dependency Management

### Automated Updates (Dependabot)

Dependabot is configured to:
- Check for updates weekly on Mondays
- Create PRs for Go modules, npm packages, GitHub Actions, and Docker images
- Label PRs appropriately

**Handling Dependabot PRs:**
1. Review the changelog for breaking changes
2. Check CI status - all tests must pass
3. For patch updates: Merge if CI passes
4. For minor/major updates: Review changes carefully
5. Test locally if significant changes

### Manual Updates

**Go Dependencies:**
```bash
go get -u ./...
go mod tidy
make test
```

**npm Dependencies:**
```bash
cd docs
pnpm update
pnpm build
```

## Security

### Vulnerability Reports

Security vulnerabilities are reported to: security@formbricks.com

**Process:**
1. Acknowledge receipt within 48 hours
2. Assess severity and impact
3. Develop and test fix
4. Coordinate disclosure timeline with reporter
5. Release patch version
6. Publish security advisory
7. Credit reporter (with permission)

### Security Advisory

Use GitHub Security Advisories:
- Draft → Review → Publish
- Include CVE information if applicable
- Provide mitigation steps
- Link to fixed version

## Community Management

### Issue Triage

Review new issues regularly:

1. **Bug Reports:**
   - Verify reproduction steps
   - Add `bug` label
   - Add component label (e.g., `api`, `enrichment`)
   - Assign priority
   - Ask for clarification if needed

2. **Feature Requests:**
   - Add `enhancement` label
   - Add component label
   - Evaluate feasibility
   - Add `needs-discussion` if unclear
   - Add `good first issue` if suitable for newcomers

3. **Questions:**
   - Direct to GitHub Discussions if not a bug/feature
   - Answer or tag relevant maintainer
   - Close and link to discussion

### Pull Request Review

- Aim to provide initial feedback within 2 business days
- Be respectful and constructive
- Explain reasoning for requested changes
- Recognize good contributions
- Merge promptly once approved

## Monitoring

### CI/CD Health

Regularly check:
- GitHub Actions workflow status
- Docker image build success
- Documentation deployment
- Dependabot PRs

### Docker Images

Monitor:
- Image size (aim to keep under 500MB)
- Security vulnerabilities (use Trivy scanning)
- Pull statistics

### Community Health

Track:
- Issue response time
- PR merge time
- Contributor growth
- Discussion activity

## Contact

For maintainer-specific questions:
- Internal team chat
- Email: team@formbricks.com
- Security: security@formbricks.com

---

*Last updated: January 2025*

