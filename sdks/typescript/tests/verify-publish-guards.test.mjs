// verify-publish-guards.mjs is the last thing between a release and the npm
// publishing identity, and the failure it exists for is silent: a publish job
// that references a missing environment creates it unprotected and succeeds. So
// these pin each configuration that must stop a publish, each one that must
// not, and the difference between the two. Several are modelled on real
// environments — pypa/pip's `PyPI`, astral-sh/ruff's `release`, psf/black's
// `release` — because those are the shapes an admin is likely to copy.
//
// No network: a local server stands in for the GitHub API.

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { after, before, test } from "node:test";

const script = fileURLToPath(
  new URL("../scripts/verify-publish-guards.mjs", import.meta.url),
);

const REPO = "formbricks/hub";
const ENV = "npm-publish";
const SHA = "36f4a63e6b0ca0aaace79fffa652fd8b72f6af48";

const root = mkdtempSync(path.join(tmpdir(), "publish-guards-"));

// Each test installs the responses it wants; anything unlisted is a 500, so a
// check that quietly skips an API call cannot pass by accident.
let routes = {};
const server = createServer((request, response) => {
  const url = new URL(request.url, "http://localhost");
  const route = routes[url.pathname];
  if (!route) {
    response.writeHead(500).end(`no route for ${url.pathname}`);
    return;
  }
  const [status, body] = route;
  response
    .writeHead(status, { "content-type": "application/json" })
    .end(JSON.stringify(body));
});

before(() => new Promise((resolve) => server.listen(0, "127.0.0.1", resolve)));
after(() => {
  server.close();
  rmSync(root, { recursive: true, force: true });
});

const environment = (overrides = {}) => ({
  name: ENV,
  can_admins_bypass: false,
  deployment_branch_policy: {
    protected_branches: false,
    custom_branch_policies: true,
  },
  protection_rules: [
    { type: "branch_policy" },
    { type: "required_reviewers", prevent_self_review: true, reviewers: [] },
  ],
  ...overrides,
});

// The configuration AGENTS.md recommends.
function recommended() {
  return {
    [`/repos/${REPO}`]: [200, { default_branch: "main" }],
    [`/repos/${REPO}/compare/main...${SHA}`]: [
      200,
      { status: "behind", ahead_by: 0 },
    ],
    [`/repos/${REPO}/environments/${ENV}`]: [200, environment()],
    [`/repos/${REPO}/environments/${ENV}/deployment-branch-policies`]: [
      200,
      { branch_policies: [{ type: "tag", name: "[0-9]*.[0-9]*.[0-9]*" }] },
    ],
  };
}

let counter = 0;

function run({ ref = "refs/tags/0.8.7", env = {} } = {}) {
  const summary = path.join(root, `summary-${counter++}.md`);
  const child = spawn(process.execPath, [script], {
    env: {
      PATH: process.env.PATH,
      GITHUB_API_URL: `http://127.0.0.1:${server.address().port}`,
      GITHUB_REPOSITORY: REPO,
      GITHUB_REF: ref,
      GITHUB_SHA: SHA,
      GITHUB_STEP_SUMMARY: summary,
      PUBLISH_ENVIRONMENT: ENV,
      ...env,
    },
  });
  let stdout = "";
  let stderr = "";
  child.stdout.on("data", (chunk) => (stdout += chunk));
  child.stderr.on("data", (chunk) => (stderr += chunk));
  return new Promise((resolve) => {
    child.on("close", (exitCode) => {
      let summaryText = "";
      try {
        summaryText = readFileSync(summary, "utf8");
      } catch {}
      resolve({
        exitCode,
        stdout,
        stderr,
        summary: summaryText,
        errors: stdout.split("\n").filter((l) => l.startsWith("::error")),
        warnings: stdout.split("\n").filter((l) => l.startsWith("::warning")),
      });
    });
  });
}

test("the recommended configuration passes cleanly", async () => {
  routes = recommended();
  const result = await run();
  assert.equal(result.exitCode, 0, result.stdout);
  assert.deepEqual(result.errors, []);
  assert.deepEqual(result.warnings, [], "nothing to warn about");
  assert.match(result.summary, /✅ .*\[0-9\]\*\.\[0-9\]\*\.\[0-9\]\*/);
});

test("a missing environment stops the publish", async () => {
  // The whole reason this exists: referencing a missing environment creates it
  // with no protection rules, and the publish would then succeed.
  routes = {
    ...recommended(),
    [`/repos/${REPO}/environments/${ENV}`]: [404, { message: "Not Found" }],
  };
  const result = await run();
  assert.equal(result.exitCode, 1);
  assert.match(result.errors.join("\n"), /does not exist/);
});

test("an environment with no restriction and no reviewers stops the publish", async () => {
  // psf/black's `release`, and what an auto-created environment looks like.
  routes = {
    ...recommended(),
    [`/repos/${REPO}/environments/${ENV}`]: [
      200,
      environment({ deployment_branch_policy: null, protection_rules: [] }),
    ],
  };
  const result = await run();
  assert.equal(result.exitCode, 1);
  assert.match(result.errors.join("\n"), /restricts neither/);
});

test('"Protected branches only" stops the publish, because it admits no tags', async () => {
  routes = {
    ...recommended(),
    [`/repos/${REPO}/environments/${ENV}`]: [
      200,
      environment({
        deployment_branch_policy: {
          protected_branches: true,
          custom_branch_policies: false,
        },
      }),
    ],
  };
  const result = await run();
  assert.equal(result.exitCode, 1);
  assert.match(result.errors.join("\n"), /admits no tags/);
});

test("a branch-only rule stops the publish, because every release is a tag", async () => {
  // astral-sh/ruff's `release` admits `main` only — right for a workflow that
  // publishes from a branch, wrong for one that publishes from tags.
  routes = {
    ...recommended(),
    [`/repos/${REPO}/environments/${ENV}/deployment-branch-policies`]: [
      200,
      { branch_policies: [{ type: "branch", name: "main" }] },
    ],
  };
  const result = await run();
  assert.equal(result.exitCode, 1);
  assert.match(result.errors.join("\n"), /admits only branches \(main\)/);
  assert.match(
    result.errors.join("\n"),
    /bare semver/,
    "and says which pattern to use, since v* is the one an admin would reach for",
  );
});

test("tags without reviewers publishes, but says why that is weak", async () => {
  // pypa/pip's `PyPI` uses the tag rule `*`, which is fine there because it
  // also requires a reviewer. Without one, the pattern is the only line.
  routes = {
    ...recommended(),
    [`/repos/${REPO}/environments/${ENV}`]: [
      200,
      environment({
        protection_rules: [{ type: "branch_policy" }],
        can_admins_bypass: true,
      }),
    ],
    [`/repos/${REPO}/environments/${ENV}/deployment-branch-policies`]: [
      200,
      { branch_policies: [{ type: "tag", name: "*" }] },
    ],
  };
  const result = await run();
  assert.equal(result.exitCode, 0, "a working configuration must not block");
  const warnings = result.warnings.join("\n");
  assert.match(warnings, /no required reviewers/);
  assert.match(warnings, /matches every tag/);
  assert.match(warnings, /Administrators can bypass/);
});

test("reviewers without a ref restriction publishes, with a warning", async () => {
  // pydantic's `release`: approval gates every run, whatever the ref.
  routes = {
    ...recommended(),
    [`/repos/${REPO}/environments/${ENV}`]: [
      200,
      environment({ deployment_branch_policy: null }),
    ],
  };
  const result = await run();
  assert.equal(result.exitCode, 0);
  assert.match(result.warnings.join("\n"), /only gate/);
});

test("reviewers who may approve their own run are flagged", async () => {
  routes = {
    ...recommended(),
    [`/repos/${REPO}/environments/${ENV}`]: [
      200,
      environment({
        protection_rules: [
          { type: "branch_policy" },
          { type: "required_reviewers", prevent_self_review: false },
        ],
      }),
    ],
  };
  const result = await run();
  assert.equal(result.exitCode, 0);
  assert.match(result.warnings.join("\n"), /Prevent self-review/);
});

test("a run for a branch stops the publish", async () => {
  // A manual run from main with dry_run unticked would otherwise publish
  // whatever main holds right now, which is not a release.
  routes = recommended();
  const result = await run({ ref: "refs/heads/main" });
  assert.equal(result.exitCode, 1);
  assert.match(result.errors.join("\n"), /not a tag/);
});

test("a tag on a commit that is not on the default branch stops the publish", async () => {
  routes = {
    ...recommended(),
    [`/repos/${REPO}/compare/main...${SHA}`]: [
      200,
      { status: "ahead", ahead_by: 3 },
    ],
  };
  const result = await run();
  assert.equal(result.exitCode, 1);
  assert.match(result.errors.join("\n"), /3 commit\(s\) ahead of main/);
});

test("an API failure blocks the publish rather than passing it", async () => {
  // An unverifiable guard is not a guard: a registry-style outage here must not
  // read as "nothing wrong".
  routes = {
    ...recommended(),
    [`/repos/${REPO}/environments/${ENV}`]: [503, { message: "unavailable" }],
  };
  const result = await run();
  assert.equal(result.exitCode, 2);
  assert.match(result.errors.join("\n"), /Could not verify/);
});

test("missing inputs block the publish", async () => {
  routes = recommended();
  const result = await run({ env: { PUBLISH_ENVIRONMENT: "" } });
  assert.equal(result.exitCode, 2);
  assert.match(result.stderr, /PUBLISH_ENVIRONMENT/);
});

test("a ref name cannot inject into the workflow command it is quoted in", async () => {
  // Branch names are user-controlled and may contain %, which the runner
  // unescapes inside a workflow command's message.
  routes = recommended();
  const result = await run({ ref: "refs/heads/100%0Ainjected" });
  assert.equal(result.exitCode, 1);
  assert.match(result.errors.join("\n"), /100%250Ainjected/);
  assert.equal(
    result.stdout.split("\n").filter((l) => l === "injected").length,
    0,
  );
});
