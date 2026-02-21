<!-- We require pull request titles to follow the Conventional Commits specification ( https://www.conventionalcommits.org/en/v1.0.0/#summary ). Please make sure your title follows these conventions -->

## What does this PR do?

<!-- Please include a summary of the change and which issue is fixed. Please also include relevant motivation and context. List any dependencies that are required for this change. -->

Fixes #(issue)

<!-- For API behavior changes, include request/response examples or screenshots of Swagger UI to speed up reviews. -->

## How should this be tested?

<!-- Please describe the tests that you ran to verify your changes. Provide instructions so we can reproduce. Please also list any relevant details for your test configuration (e.g. DATABASE_URL, API_KEY). -->

- Test A
- Test B

## Checklist

<!-- Please go through this checklist and tick off what you did. -->

### Required

- [ ] Filled out the "How to test" section in this PR
- [ ] Read [Repository Guidelines](https://github.com/formbricks/hub/blob/main/AGENTS.md)
- [ ] Self-reviewed my own code
- [ ] Commented on my code in hard-to-understand bits
- [ ] Ran `make build`
- [ ] Ran `make tests` (integration tests in `tests/`)
- [ ] Ran `make fmt` and `make lint`; no new warnings
- [ ] Removed debug prints / temporary logging
- [ ] Merged the latest changes from main onto my branch with `git pull origin main`
- [ ] If database schema changed: added migration in `migrations/` with goose annotations and ran `make migrate-validate`

### Appreciated

- [ ] If API changed: added or updated OpenAPI spec and ran contract tests (`make tests` or API contract workflow)
- [ ] If API behavior changed: added request/response examples or Swagger UI screenshots to this PR
- [ ] Updated docs in `docs/` if changes were necessary
- [ ] Ran `make tests-coverage` for meaningful logic changes
