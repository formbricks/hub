// resolve-latest-release.sh decides whether a release may move GHCR `latest` and the ECR latest and
// production tags (ENG-2927). These drive the real script and the real curl against a local
// stand-in for the GitHub API, so the retry behaviour is curl's own rather than a mock's idea of it.
// formbricks/formbricks runs the same cases against its twin of the script.
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
    seen.push({ url: request.url, headers: request.headers });
    const next = queue.shift() ?? { status: 599, body: "stub queue exhausted" };
    response.writeHead(next.status, { "content-type": "application/json" });
    response.end(next.body);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  baseUrl = `http://127.0.0.1:${server.address().port}`;
  workDir = mkdtempSync(path.join(tmpdir(), "resolve-latest-release-"));
});

after(async () => {
  await new Promise((resolve) => server.close(resolve));
  rmSync(workDir, { recursive: true, force: true });
});

afterEach(() => {
  queue = [];
  seen = [];
});

const respond = (...responses) => {
  queue = responses;
};

const latest = (tagName) => ({ status: 200, body: JSON.stringify({ tag_name: tagName }) });

const resolveLatest = async (currentTag, { apiUrl = baseUrl, pathEnv = process.env.PATH } = {}) => {
  const outputFile = path.join(workDir, `output-${++runs}`);
  writeFileSync(outputFile, "");

  const child = spawn("bash", [script], {
    env: {
      PATH: pathEnv,
      CURRENT_TAG: currentTag,
      GITHUB_TOKEN: token,
      GITHUB_REPOSITORY: repository,
      GITHUB_API_URL: apiUrl,
      GITHUB_OUTPUT: outputFile,
      RELEASE_LOOKUP_RETRIES: "1",
    },
  });
  let log = "";
  child.stdout.on("data", (chunk) => (log += chunk.toString()));
  child.stderr.on("data", (chunk) => (log += chunk.toString()));
  const status = await new Promise((done) => child.on("close", done));

  return { status, log, output: readFileSync(outputFile, "utf8") };
};

test("promotes the release GitHub marks as latest", async () => {
  respond(latest("0.9.0"));

  const result = await resolveLatest("0.9.0");

  assert.equal(result.status, 0);
  assert.equal(result.output, "is_latest=true\n");
});

test("does not promote a release when another one is latest", async () => {
  respond(latest("0.9.0"));

  const result = await resolveLatest("0.8.8");

  assert.equal(result.status, 0);
  assert.equal(result.output, "is_latest=false\n");
});

test("asks for this repository's latest release with the job token", async () => {
  respond(latest("0.9.0"));

  await resolveLatest("0.9.0");

  assert.equal(seen.length, 1);
  assert.equal(seen[0].url, `/repos/${repository}/releases/latest`);
  assert.equal(seen[0].headers.authorization, `Bearer ${token}`);
});

test("does not promote when GitHub marks no release as latest", async () => {
  respond({ status: 404, body: JSON.stringify({ message: "Not Found" }) });

  const result = await resolveLatest("0.8.8");

  assert.equal(result.status, 0);
  assert.equal(result.output, "is_latest=false\n");
});

for (const [label, body] of [
  ["no tag_name", JSON.stringify({})],
  ["a null tag_name", JSON.stringify({ tag_name: null })],
  ["an empty tag_name", JSON.stringify({ tag_name: "" })],
  ["a non-string tag_name", JSON.stringify({ tag_name: 5 })],
  ["a body that is not JSON", "<html>unicorn</html>"],
]) {
  test(`fails instead of deciding on a 200 with ${label}`, async () => {
    respond({ status: 200, body });

    const result = await resolveLatest("0.8.8");

    assert.equal(result.status, 1);
    assert.equal(result.output, "");
  });
}

// The old step wrote the API's tag_name into $GITHUB_OUTPUT, where a newline forges a second output.
test("never lets a tag_name forge a workflow output", async () => {
  respond(latest("0.8.8\nis_latest=true"));

  const result = await resolveLatest("0.8.8");

  assert.equal(result.status, 1);
  assert.equal(result.output, "");
});

test("fails on 403 without retrying it", async () => {
  respond({ status: 403, body: JSON.stringify({ message: "Resource not accessible by integration" }) });

  const result = await resolveLatest("0.8.8");

  assert.equal(result.status, 1);
  assert.equal(result.output, "");
  assert.equal(seen.length, 1);
  assert.match(result.log, /HTTP 403: Resource not accessible by integration/);
});

for (const status of [429, 500, 502, 503]) {
  test(`fails once HTTP ${status} outlasts the retries`, async () => {
    respond({ status, body: "{}" }, { status, body: "{}" });

    const result = await resolveLatest("0.8.8");

    assert.equal(result.status, 1);
    assert.equal(result.output, "");
    // One attempt plus RELEASE_LOOKUP_RETRIES=1.
    assert.equal(seen.length, 2);
  });
}

test("decides once a transient failure clears", async () => {
  respond({ status: 503, body: "{}" }, latest("0.9.0"));

  const result = await resolveLatest("0.9.0");

  assert.equal(result.status, 0);
  assert.equal(result.output, "is_latest=true\n");
  assert.equal(seen.length, 2);
});

test("fails when the API cannot be reached", async () => {
  // A port that was just released has no listener, so the connection is refused.
  const closed = createServer();
  await new Promise((resolve) => closed.listen(0, "127.0.0.1", resolve));
  const { port } = closed.address();
  await new Promise((resolve) => closed.close(resolve));

  const result = await resolveLatest("0.8.8", { apiUrl: `http://127.0.0.1:${port}` });

  assert.equal(result.status, 1);
  assert.equal(result.output, "");
  assert.match(result.log, /Could not reach the GitHub releases API/);
});

test("never echoes an error body into the log, where Actions would parse it", async () => {
  respond(
    { status: 500, body: "::warning::injected\n::add-mask::x" },
    { status: 500, body: "::warning::injected" }
  );

  const result = await resolveLatest("0.8.8");

  assert.equal(result.status, 1);
  assert.doesNotMatch(result.log, /::warning::|::add-mask::/);
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
  respond(latest("0.9.0"));

  const result = await resolveLatest("0.9.0", { pathEnv: `${wrapperDir}:${process.env.PATH}` });

  assert.equal(result.output, "is_latest=true\n");
  assert.equal(seen[0].headers.authorization, `Bearer ${token}`);
  assert.ok(!readFileSync(argvFile, "utf8").includes(token));
});
