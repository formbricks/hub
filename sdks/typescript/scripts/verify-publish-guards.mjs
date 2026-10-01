#!/usr/bin/env node

/**
 * Checks, before an npm publish, that the publish can only happen the way it is
 * meant to. It runs in its own job in publish-sdk.yml — first-party code only,
 * read-only token — and the publish job needs it to pass.
 *
 * Three checks, each closing a way a legitimate-looking run could publish what
 * it should not:
 *
 *   1. The run is for a tag. Every Hub release is a tag, so a publish from a
 *      branch — a manual run with dry_run unticked, say — is a mistake.
 *   2. The tagged commit is reachable from the default branch, which is where
 *      review happens. A release can be created at any commit, and provenance
 *      would then faithfully attest code nobody reviewed.
 *   3. The publish environment exists and restricts its use in a way that still
 *      admits a release. npm's trusted-publisher binding covers the repository,
 *      the workflow file and the environment name, but not the ref, so the
 *      environment is what stands between an arbitrary ref and the publishing
 *      identity. A workflow that references a missing environment creates it on
 *      the spot with no protection rules at all; failing here first means the
 *      publish job never gets the chance.
 *
 * What this cannot do: anyone who can push a commit can also edit this check
 * out of the workflow at that commit. So it catches honest mistakes and
 * misconfiguration. Resisting a malicious committer is the environment's job —
 * required reviewers, or a ruleset on release tags — which is why a
 * configuration that works but lacks those is reported as a warning rather than
 * accepted silently.
 *
 * Reads the standard Actions variables (GITHUB_API_URL, GITHUB_REPOSITORY,
 * GITHUB_REF, GITHUB_SHA, GITHUB_STEP_SUMMARY), plus GITHUB_TOKEN, and
 * PUBLISH_ENVIRONMENT for the environment's name.
 *
 * Exits 0 when every check passes (warnings may still be printed), 1 when a
 * check fails, and 2 when a check cannot be carried out at all — which also
 * blocks the publish, because an unverifiable guard is not a guard.
 */

import { appendFileSync } from "node:fs";

const {
  GITHUB_API_URL = "https://api.github.com",
  GITHUB_REPOSITORY,
  GITHUB_REF,
  GITHUB_SHA,
  GITHUB_TOKEN,
  GITHUB_STEP_SUMMARY,
  PUBLISH_ENVIRONMENT,
} = process.env;

const unset = Object.entries({
  GITHUB_REPOSITORY,
  GITHUB_REF,
  GITHUB_SHA,
  PUBLISH_ENVIRONMENT,
})
  .filter(([, value]) => !value)
  .map(([name]) => name);

if (unset.length > 0) {
  console.error(`Cannot verify: ${unset.join(", ")} not set.`);
  process.exit(2);
}

// Workflow commands read their message up to the end of the line, and the runner
// unescapes these three sequences in it. A ref name is user-controlled, so it is
// escaped before it ends up inside one.
const escapeData = (value) =>
  value.replaceAll("%", "%25").replaceAll("\r", "%0D").replaceAll("\n", "%0A");

async function api(path, { allowNotFound = false } = {}) {
  const response = await fetch(`${GITHUB_API_URL}${path}`, {
    headers: {
      accept: "application/vnd.github+json",
      "x-github-api-version": "2022-11-28",
      ...(GITHUB_TOKEN ? { authorization: `Bearer ${GITHUB_TOKEN}` } : {}),
    },
  });
  if (response.status === 404 && allowNotFound) return null;
  if (!response.ok) {
    const body = (await response.text()).slice(0, 300);
    throw new Error(`GET ${path} returned ${response.status}: ${body}`);
  }
  return response.json();
}

const passed = [];
const warnings = [];
const failures = [];

function checkRef() {
  if (GITHUB_REF.startsWith("refs/tags/")) {
    passed.push(
      `the run is for the tag ${GITHUB_REF.slice("refs/tags/".length)}`,
    );
    return;
  }
  failures.push(
    `The run is for ${GITHUB_REF}, not a tag. Only a release tag publishes; to publish a release again, run the workflow from that tag.`,
  );
}

async function checkReachableFromDefaultBranch() {
  const { default_branch: defaultBranch } = await api(
    `/repos/${GITHUB_REPOSITORY}`,
  );
  const comparison = await api(
    `/repos/${GITHUB_REPOSITORY}/compare/${defaultBranch}...${GITHUB_SHA}`,
  );
  if (typeof comparison.ahead_by !== "number") {
    throw new Error("the compare response carried no ahead_by count");
  }
  // "identical" and "behind" both mean the commit is already part of the default
  // branch's history; anything ahead of it has not been merged there.
  if (comparison.ahead_by === 0) {
    passed.push(`${GITHUB_SHA.slice(0, 12)} is on ${defaultBranch}`);
    return;
  }
  failures.push(
    `${GITHUB_SHA.slice(0, 12)} is ${comparison.ahead_by} commit(s) ahead of ${defaultBranch}, so it never went through review there. Release from a commit on ${defaultBranch}.`,
  );
}

async function checkEnvironment() {
  const name = PUBLISH_ENVIRONMENT;
  const environment = await api(
    `/repos/${GITHUB_REPOSITORY}/environments/${encodeURIComponent(name)}`,
    { allowNotFound: true },
  );

  if (!environment) {
    failures.push(
      `The ${name} environment does not exist. The publish job would create it on first use with no protection rules, leaving any ref free to obtain the publishing identity. Create and protect it first — see AGENTS.md, "TypeScript SDK".`,
    );
    return;
  }

  // null means "No restriction": any branch or tag may deploy.
  const policy = environment.deployment_branch_policy;
  const reviewers = (environment.protection_rules ?? []).find(
    (rule) => rule.type === "required_reviewers",
  );

  if (!policy && !reviewers) {
    failures.push(
      `The ${name} environment restricts neither which refs may use it nor who must approve — the same state as one created automatically. Restrict it to release tags, add required reviewers, or both.`,
    );
    return;
  }

  if (policy?.protected_branches) {
    // GitHub's "Protected branches only" admits branches and nothing else, so
    // with a tag-triggered publish it would reject every release.
    failures.push(
      `The ${name} environment allows "Protected branches only", which admits no tags — and every release is a tag, so nothing could ever publish. Use "Selected branches and tags" with a tag rule instead.`,
    );
    return;
  }

  let tagRules = [];
  if (policy?.custom_branch_policies) {
    const { branch_policies: rules = [] } = await api(
      `/repos/${GITHUB_REPOSITORY}/environments/${encodeURIComponent(name)}/deployment-branch-policies?per_page=100`,
    );
    tagRules = rules.filter((rule) => rule.type === "tag");
    if (tagRules.length === 0) {
      failures.push(
        `The ${name} environment admits only branches (${rules.map((rule) => rule.name).join(", ") || "none"}), and every release is a tag, so nothing could ever publish. Add a tag rule matching the release tags — they are bare semver, so [0-9]*.[0-9]*.[0-9]* rather than v*.`,
      );
      return;
    }
    passed.push(
      `${name} admits only the tag patterns ${tagRules.map((rule) => rule.name).join(", ")}`,
    );
  } else {
    warnings.push(
      `The ${name} environment does not restrict which refs may use it; required reviewers are its only gate.`,
    );
  }

  if (reviewers) {
    passed.push(`${name} requires a reviewer to approve each publish`);
    if (!reviewers.prevent_self_review) {
      warnings.push(
        `The ${name} environment lets the person who started a run approve it. Enable "Prevent self-review", so approval is a second person's.`,
      );
    }
  } else {
    const matchesEveryTag = tagRules.some((rule) => rule.name === "*");
    warnings.push(
      `The ${name} environment has no required reviewers, so anyone who can create a matching tag can publish without a second person${
        matchesEveryTag ? " — and its tag rule * matches every tag" : ""
      }. Add required reviewers with "Prevent self-review", or a ruleset limiting who may create release tags.`,
    );
  }

  if (environment.can_admins_bypass) {
    warnings.push(
      `Administrators can bypass the ${name} environment's rules. Untick "Allow administrators to bypass configured protection rules" unless that is intended.`,
    );
  }
}

try {
  checkRef();
  await checkReachableFromDefaultBranch();
  await checkEnvironment();
} catch (error) {
  console.log(
    `::error title=Publish guards::${escapeData(`Could not verify the publish guards: ${error.message}`)}`,
  );
  process.exit(2);
}

for (const line of passed) console.log(`✓ ${line}`);
for (const line of warnings) {
  console.log(`::warning title=Publish guards::${escapeData(line)}`);
}
for (const line of failures) {
  console.log(`::error title=Publish guards::${escapeData(line)}`);
}

if (GITHUB_STEP_SUMMARY) {
  const summary = [
    "## Publish guards",
    "",
    ...passed.map((line) => `- ✅ ${line}`),
    ...warnings.map((line) => `- ⚠️ ${line}`),
    ...failures.map((line) => `- ❌ ${line}`),
    "",
  ].join("\n");
  appendFileSync(GITHUB_STEP_SUMMARY, `${summary}\n`);
}

process.exit(failures.length > 0 ? 1 : 0);
