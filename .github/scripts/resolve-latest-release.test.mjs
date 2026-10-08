// resolve-latest-release.sh decides whether a release may move GHCR `latest` and the ECR latest and
// production tags (ENG-2927). These drive the real script and the real curl against a local
// stand-in for GitHub's GraphQL API, so the retry behaviour is curl's own rather than a mock's idea
// of it. formbricks/formbricks runs the same cases against its twin of the script.
//
// No network: everything stays on 127.0.0.1.

import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, test } from "node:test";

const script = fileURLToPath(new URL("./resolve-latest-release.sh", import.meta.url));
const repository = "formbricks/hub";
// A fixed dummy, never the caller's GITHUB_TOKEN: the stub records the header and a failing
// assertion would print it.
const token = "test-token-not-a-secret";

let server;
let baseUrl;
let queue = [];
let seen = [];
let workDir;
let runs = 0;

before(async () => {
  server = createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => (body += chunk.toString()));
    request.on("end", () => {
      seen.push({ method: request.method, url: request.url, headers: request.headers, body });
      const next = queue.shift() ?? { status: 599, body: "stub queue exhausted" };
      // `hang` accepts the request and never answers, to exercise curl's per-attempt timeout.
      if (next.hang) return;
      response.writeHead(next.status, { "content-type": "application/json" });
      response.end(next.body);
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  baseUrl = `http://127.0.0.1:${server.address().port}`;
  workDir = mkdtempSync(path.join(tmpdir(), "resolve-latest-release-"));
});

after(async () => {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  rmSync(workDir, { recursive: true, force: true });
});

afterEach(() => {
  server.closeAllConnections();
  queue = [];
  seen = [];
});

const respond = (...responses) => {
  queue = responses;
};

const graphql = (payload) => ({ status: 200, body: JSON.stringify(payload) });

const release = (tagName, isLatest) => graphql({ data: { repository: { release: { tagName, isLatest } } } });

const resolveLatest = async (
  currentTag,
  { apiUrl = `${baseUrl}/graphql`, pathEnv = process.env.PATH, timeout = "20" } = {}
) => {
  const outputFile = path.join(workDir, `output-${++runs}`);
  writeFileSync(outputFile, "");

  const child = spawn("bash", [script], {
    env: {
      PATH: pathEnv,
      CURRENT_TAG: currentTag,
      GITHUB_TOKEN: token,
      GITHUB_REPOSITORY: repository,
      GITHUB_GRAPHQL_URL: apiUrl,
      GITHUB_OUTPUT: outputFile,
      RELEASE_LOOKUP_RETRIES: "1",
      RELEASE_LOOKUP_TIMEOUT: timeout,
    },
  });
  let log = "";
  child.stdout.on("data", (chunk) => (log += chunk.toString()));
  child.stderr.on("data", (chunk) => (log += chunk.toString()));
  const status = await new Promise((done) => child.on("close", done));

  return { status, log, output: readFileSync(outputFile, "utf8") };
};

test("promotes the release GitHub marks as latest", async () => {
  respond(release("0.9.0", true));

  const result = await resolveLatest("0.9.0");

  assert.equal(result.status, 0);
  assert.equal(result.output, "is_latest=true\n");
});

test("does not promote a release GitHub does not mark as latest", async () => {
  respond(release("0.8.8", false));

  const result = await resolveLatest("0.8.8");

  assert.equal(result.status, 0);
  assert.equal(result.output, "is_latest=false\n");
});

test("asks for this release with the job token, passing the tag as a variable", async () => {
  respond(release("0.9.0", true));

  await resolveLatest("0.9.0");

  assert.equal(seen.length, 1);
  assert.equal(seen[0].method, "POST");
  assert.equal(seen[0].url, "/graphql");
  assert.equal(seen[0].headers.authorization, `Bearer ${token}`);
  const request = JSON.parse(seen[0].body);
  assert.deepEqual(request.variables, { owner: "formbricks", name: "hub", tag: "0.9.0" });
  assert.match(request.query, /release\(tagName: \$tag\)/);
  assert.ok(!request.query.includes("0.9.0"));
});

test("keeps a hostile tag out of the query text", async () => {
  const tag = '0.9.0") { id } evil: viewer { login } #';
  respond(release(tag, true));

  const result = await resolveLatest(tag);

  assert.equal(result.output, "is_latest=true\n");
  assert.equal(JSON.parse(seen[0].body).variables.tag, tag);
  assert.ok(!JSON.parse(seen[0].body).query.includes("viewer"));
});

for (const [label, response] of [
  [
    "the repository is not visible",
    graphql({
      data: { repository: null },
      errors: [{ type: "NOT_FOUND", message: "Could not resolve to a Repository" }],
    }),
  ],
  ["the release does not exist", graphql({ data: { repository: { release: null } } })],
  ["a rate limit", graphql({ errors: [{ type: "RATE_LIMITED", message: "API rate limit exceeded" }] })],
  [
    "an error beside partial data",
    graphql({
      data: { repository: { release: { tagName: "0.9.0", isLatest: true } } },
      errors: [{ type: "FORBIDDEN", message: "Resource not accessible" }],
    }),
  ],
  ["another release's answer", release("0.8.7", true)],
  ["a missing isLatest", graphql({ data: { repository: { release: { tagName: "0.9.0" } } } })],
  ["a non-boolean isLatest", release("0.9.0", "true")],
  ["a null isLatest", release("0.9.0", null)],
  ["no data", graphql({})],
  ["an array", graphql([{ data: { repository: { release: { tagName: "0.9.0", isLatest: true } } } }])],
  ["a body that is not JSON", { status: 200, body: "<html>unicorn</html>" }],
  ["an empty body", { status: 200, body: "" }],
  // jq reads a body as a stream; a second document must not let the first one through.
  [
    "two JSON documents",
    {
      status: 200,
      body: `${JSON.stringify({ data: { repository: { release: { tagName: "0.9.0", isLatest: true } } } })}{}`,
    },
  ],
]) {
  test(`fails instead of deciding on ${label}`, async () => {
    respond(response);

    const result = await resolveLatest("0.9.0");

    assert.equal(result.status, 1);
    assert.equal(result.output, "");
  });
}

for (const status of [401, 403]) {
  test(`fails on HTTP ${status} without retrying it`, async () => {
    respond({ status, body: JSON.stringify({ message: "Bad credentials" }) });

    const result = await resolveLatest("0.9.0");

    assert.equal(result.status, 1);
    assert.equal(result.output, "");
    assert.equal(seen.length, 1);
    assert.ok(result.log.includes(`HTTP ${status}: Bad credentials`));
  });
}

for (const status of [429, 500, 502, 503]) {
  test(`fails once HTTP ${status} outlasts the retries`, async () => {
    respond({ status, body: "{}" }, { status, body: "{}" });

    const result = await resolveLatest("0.9.0");

    assert.equal(result.status, 1);
    assert.equal(result.output, "");
    // One attempt plus RELEASE_LOOKUP_RETRIES=1.
    assert.equal(seen.length, 2);
  });
}

test("decides once a transient failure clears", async () => {
  respond({ status: 503, body: "{}" }, release("0.9.0", true));

  const result = await resolveLatest("0.9.0");

  assert.equal(result.status, 0);
  assert.equal(result.output, "is_latest=true\n");
  // The retry repeats the same authenticated query.
  assert.deepEqual(
    seen.map((request) => request.headers.authorization),
    [`Bearer ${token}`, `Bearer ${token}`]
  );
  assert.equal(seen[1].body, seen[0].body);
});

test("fails when every attempt times out", async () => {
  respond({ hang: true }, { hang: true });

  const result = await resolveLatest("0.9.0", { timeout: "1" });

  assert.equal(result.status, 1);
  assert.equal(result.output, "");
  assert.equal(seen.length, 2);
  assert.match(result.log, /curl exit 28/);
});

test("fails when the API cannot be reached", async () => {
  // A port that was just released has no listener, so the connection is refused.
  const closed = createServer();
  await new Promise((resolve) => closed.listen(0, "127.0.0.1", resolve));
  const { port } = closed.address();
  await new Promise((resolve) => closed.close(resolve));

  const result = await resolveLatest("0.9.0", { apiUrl: `http://127.0.0.1:${port}/graphql` });

  assert.equal(result.status, 1);
  assert.equal(result.output, "");
  assert.match(result.log, /Could not reach the GitHub API/);
});

test("never lets a response start a log line, where Actions would parse it", async () => {
  respond(
    { status: 500, body: "::warning::injected\n::add-mask::x" },
    { status: 500, body: "::warning::injected\n::add-mask::x" }
  );
  const raw = await resolveLatest("0.9.0");
  respond(graphql({ errors: [{ message: "boom\n::warning::injected" }] }));
  const message = await resolveLatest("0.9.0");

  for (const result of [raw, message]) {
    assert.equal(result.status, 1);
    assert.deepEqual(
      result.log.split("\n").filter((line) => /^::(warning|add-mask)::/.test(line)),
      []
    );
  }
  assert.ok(!raw.log.includes("::add-mask::"));
});

test("keeps the token out of curl's command line", async () => {
  // ps shows every process's argv on the runner, so the token must not be an argument. A wrapper
  // ahead of the real curl on PATH records the arguments it was given.
  const realCurl = execFileSync("bash", ["-c", "command -v curl"], { encoding: "utf8" }).trim();
  const wrapperDir = mkdtempSync(path.join(workDir, "bin-"));
  const argvFile = path.join(wrapperDir, "argv");
  writeFileSync(
    path.join(wrapperDir, "curl"),
    `#!/usr/bin/env bash\nprintf '%s\\n' "$@" > '${argvFile}'\nexec '${realCurl}' "$@"\n`,
    { mode: 0o755 }
  );
  respond(release("0.9.0", true));

  const result = await resolveLatest("0.9.0", { pathEnv: `${wrapperDir}:${process.env.PATH}` });

  assert.equal(result.output, "is_latest=true\n");
  assert.equal(seen[0].headers.authorization, `Bearer ${token}`);
  assert.ok(!readFileSync(argvFile, "utf8").includes(token));
});
