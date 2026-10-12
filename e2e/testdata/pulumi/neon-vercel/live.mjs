import * as fs from "node:fs";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import assert from "node:assert/strict";
import YAML from "yaml";
import { scaffold, workspace, pm, command } from "./scaffold.mjs";

if (process.env.CI !== "true")
  throw new Error("Live provider administration runs in CI using CI-only credentials");
for (const key of ["NEON_API_KEY", "VERCEL_TEAM_ID", "PULUMI_BACKEND_URL"]) {
  if (!process.env[key]) throw new Error(`${key} is required`);
}
if (!process.env.VERCEL_API_TOKEN && !process.env.VERCEL_TOKEN)
  throw new Error("Vercel CI credential is required");
process.env.PREMISE_RC ??= process.env.PREMISE_RC_VERSION;
if (!process.env.PREMISE_RC || !/^v.*-rc\./.test(process.env.PREMISE_RC))
  throw new Error("PREMISE_RC_VERSION must identify the verified published CLI release under test");
const binary = path.isAbsolute(pm)
  ? pm
  : spawnSync("which", [pm], { encoding: "utf8" }).stdout.trim();
const binaryDigest = createHash("sha256")
  .update(fs.readFileSync(fs.realpathSync(binary)))
  .digest("hex");
if (process.env.PREMISE_RC_SHA256 && binaryDigest !== process.env.PREMISE_RC_SHA256)
  throw new Error("Installed Premise binary differs from verified published RC checksum");
scaffold();
const app = path.join(workspace, "apps/web");
const program = path.join(workspace, "infra/environment");
const requireApp = createRequire(path.join(app, "package.json"));
const { Client } = requireApp("pg");
const branch =
  process.env.PREMISE_PREVIEW_BRANCH || process.env.GITHUB_HEAD_REF || process.env.GITHUB_REF_NAME;
if (!branch || branch === "main") throw new Error("Use a feature-branch preview identity");
let scope = ["--env", "preview", "--branch", branch];
const project = process.env.VERCEL_PROJECT_ID;
const evidence = {
  cli: process.env.PREMISE_RC,
  source: process.env.GITHUB_SHA,
  binary_sha256: binaryDigest,
  branch,
  assertions: [],
};
const actionPath = process.env.PREMISE_ACTION_PATH;
if (!actionPath)
  throw new Error("PREMISE_ACTION_PATH must identify the checked-out published action");
const actionSteps = YAML.parse(fs.readFileSync(path.join(actionPath, "action.yml"), "utf8")).runs
  .steps;
evidence.action_source = capture("git", ["rev-parse", "HEAD"], actionPath).trim();
const retainedDeployments = new Set();
const branches = new Set();
const aliases = new Set();
let stack;
let deploymentStarted = false;
let originalFailure;
const cleanupScopes = [];

function capture(name, args, cwd = workspace, env = {}) {
  const result = spawnSync(name, args, {
    cwd,
    env: { ...process.env, ...env },
    encoding: "utf8",
  });
  if (result.status !== 0) throw new Error(`${name} failed; captured output is private`);
  return result.stdout;
}
function publicOutputs() {
  return JSON.parse(capture(pm, ["infra", "output", ...scope]));
}
function sourceIdentity() {
  const hash = createHash("sha256");
  const skip = new Set(["node_modules", "dist", ".output", ".git", ".vercel", "sdks", ".pulumi"]);
  function visit(directory, prefix) {
    for (const entry of fs
      .readdirSync(directory, { withFileTypes: true })
      .sort((a, b) => a.name.localeCompare(b.name))) {
      if (
        skip.has(entry.name) ||
        entry.name.startsWith(".env") ||
        (entry.name.startsWith("Pulumi.") && entry.name !== "Pulumi.yaml")
      )
        continue;
      const filename = path.join(directory, entry.name);
      const relative = path.join(prefix, entry.name);
      if (entry.isDirectory()) visit(filename, relative);
      else if (entry.isFile()) {
        hash.update(relative);
        hash.update(fs.readFileSync(filename));
      } else throw new Error("Immutable source cannot include symlinks");
    }
  }
  visit(app, "app");
  visit(program, "infra");
  visit(path.join(workspace, "infra/components"), "components");
  return hash.digest("hex");
}

function records() {
  const plan = JSON.parse(capture(pm, ["deploy", "plan", "--json", ...scope]));
  stack = plan.stack;
  return {
    directory: plan.records_directory,
    remote: `${plan.backend_url}/.premise-releases/${plan.program}/${plan.stack}`,
    archive: `${plan.backend_url}/.premise-release-history/${process.env.GITHUB_RUN_ID}/${plan.program}/${plan.stack}`,
  };
}
function runActionStep(name, environment) {
  const step = actionSteps.find((step) => step.name === name);
  if (!step?.run || step.run.includes("${{"))
    throw new Error(
      `Published deployment step ${name} is missing or has an unsupported inline expression`,
    );
  return spawnSync("bash", ["-c", step.run], {
    cwd: workspace,
    env: { ...process.env, ...environment },
    stdio: "inherit",
  }).status;
}

function runRelease(release, fault = {}) {
  deploymentStarted = true;
  if (!cleanupScopes.some((existing) => JSON.stringify(existing) === JSON.stringify(scope)))
    cleanupScopes.push([...scope]);
  const environmentFile = path.join(workspace, ".premise/action-environment");
  fs.mkdirSync(path.dirname(environmentFile), { recursive: true });
  fs.writeFileSync(environmentFile, "", { mode: 0o600 });
  const environment = {
    ...fault,
    COMMAND: "deploy",
    FLOW: "on-commit",
    RELEASE_MODE: "none",
    DEPLOY_ENVIRONMENT: scope[1],
    PREVIEW_BRANCH: scope[3],
    INFRA_BACKEND: "remote",
    RECORDS_URL: "",
    DEPLOY_REVISION: release,
    DEPLOY_ARTIFACT: `web=${sourceIdentity()}`,
    GITHUB_ENV: environmentFile,
  };
  if (runActionStep("Restore deployment recovery records", environment) !== 0)
    throw new Error("Published action could not restore recovery records");
  for (const line of fs.readFileSync(environmentFile, "utf8").trim().split("\n")) {
    const separator = line.indexOf("=");
    const key = line.slice(0, separator);
    if (!["PM_RECORDS_DIRECTORY", "PM_RECORDS_URL"].includes(key))
      throw new Error("Published action wrote an unexpected deployment environment key");
    environment[key] = line.slice(separator + 1);
  }
  const result = {
    status: runActionStep("Run Premise operation", environment),
  };
  if (runActionStep("Persist deployment recovery records", environment) !== 0)
    throw new Error("Published action could not persist recovery records");
  const state = records();
  const run = JSON.parse(fs.readFileSync(path.join(state.directory, "last-run.json")));
  const lastFile = path.join(state.directory, "last-success.json");
  const last = fs.existsSync(lastFile) ? JSON.parse(fs.readFileSync(lastFile)) : {};
  let outputs = {};
  try {
    outputs = publicOutputs();
  } catch (error) {
    if (result.status === 0) throw error;
  }
  if (outputs.adminDeploymentId) retainedDeployments.add(outputs.adminDeploymentId);
  if (outputs.adminAliasName) aliases.add(outputs.adminAliasName);
  if (outputs.deploymentId) retainedDeployments.add(outputs.deploymentId);
  if (outputs.databaseBranchId) branches.add(outputs.databaseBranchId);
  if (outputs.aliasName) aliases.add(outputs.aliasName);
  if (last.outputs?.deploymentId) retainedDeployments.add(last.outputs.deploymentId);
  return { status: result.status, run, last, outputs };
}
async function request(url, options) {
  const response = await fetch(`${url}/api/e2e`, {
    ...options,
    headers: {
      ...options?.headers,
      ...(process.env.PREMISE_E2E_BYPASS_SECRET
        ? {
            "x-vercel-protection-bypass": process.env.PREMISE_E2E_BYPASS_SECRET,
          }
        : {}),
    },
    signal: AbortSignal.timeout(30000),
  });
  assert.equal(response.status, 200, "application endpoint must succeed");
  return response.json();
}
async function vercel(method, endpoint, body) {
  const url = new URL(`https://api.vercel.com${endpoint}`);
  url.searchParams.set("teamId", process.env.VERCEL_TEAM_ID);
  const response = await fetch(url, {
    method,
    headers: {
      Authorization: `Bearer ${process.env.VERCEL_API_TOKEN ?? process.env.VERCEL_TOKEN}`,
      "Content-Type": "application/json",
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  return response;
}
async function sqlAssertions(expectedSchema) {
  // Secret output is consumed in memory and never written to evidence or stdout.
  const connection = capture(
    "pulumi",
    ["stack", "output", "databaseURL", "--stack", stack, "--show-secrets"],
    program,
  ).trim();
  const client = new Client({ connectionString: connection });
  await client.connect();
  try {
    const migrationCount = await client.query(
      "SELECT count(*)::int AS count FROM drizzle.__drizzle_migrations",
    );
    assert.equal(migrationCount.rows[0].count, expectedSchema === "B" ? 2 : 1);
    if (expectedSchema === "B") {
      const column = await client.query(
        "SELECT column_name FROM information_schema.columns WHERE table_name = 'premise_e2e' AND column_name = 'notes'",
      );
      assert.equal(column.rows.length, 1, "additive migration must reach Neon");
      const sentinel = await client.query("SELECT value FROM premise_e2e WHERE id = 'sentinel'");
      assert.equal(sentinel.rows[0].value, "preserved");
    }
  } finally {
    await client.end();
  }
}

try {
  const preflight = await vercel("GET", `/v9/projects/${project}`);
  assert.equal(preflight.status, 200, "dedicated Vercel project must be accessible");
  const projectConfig = await preflight.json();
  if (projectConfig.ssoProtection && !process.env.PREMISE_E2E_BYPASS_SECRET)
    throw new Error("Configure test-project protection bypass before running e2e");
  fs.writeFileSync(path.join(app, ".env.local"), "LOCAL_ONLY_SENTINEL=must-never-upload\n", {
    mode: 0o600,
  });
  const a = runRelease("A");
  assert.equal(a.status, 0);
  assert.equal(a.last.release, "A");
  assert.equal(
    a.outputs.clientLabel,
    stack,
    "environment must actually compose the client-owned component",
  );
  command(
    pm,
    ["secrets", "pull", "--env", "preview", "--branch", branch, "--project", "web"],
    workspace,
  );
  const pulled = fs.readFileSync(path.join(app, ".env.local"), "utf8");
  assert.ok(pulled.includes("DATABASE_URL="));
  assert.ok(pulled.includes("APP_MARKER="));
  evidence.assertions.push(
    "Pulumi provisioned branch-owned Secret Manager values; fnox delivered migration inputs and a private local env file",
  );
  await sqlAssertions("A");
  await request(a.outputs.appURL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: "sentinel", value: "preserved" }),
  });
  assert.equal((await request(a.outputs.appURL)).release, "A");
  evidence.assertions.push(
    "A deployed, initial migration recorded, sentinel written through app; ignored local env excluded by provider file-map guard",
  );

  const repeated = runRelease("A");
  assert.equal(repeated.status, 0);
  assert.notEqual(a.outputs.adminDeploymentId, a.outputs.deploymentId);
  assert.notEqual(a.outputs.adminAliasName, a.outputs.aliasName);
  await request(a.outputs.adminURL, { method: "GET" });
  evidence.assertions.push(
    "Two independently rendered Vercel components deploy separate app bindings in one stack",
  );
  assert.equal(repeated.outputs.databaseBranchId, a.outputs.databaseBranchId);
  assert.equal(repeated.outputs.deploymentId, a.outputs.deploymentId);
  await sqlAssertions("A");
  evidence.assertions.push(
    "No-change reapply reused the database/deployment and did not duplicate migrations",
  );
  const primaryScope = [...scope];
  scope = ["--env", "preview", "--branch", branch + "-isolation"];
  const isolated = runRelease("A");
  assert.equal(isolated.status, 0);
  assert.notEqual(isolated.outputs.databaseBranchId, a.outputs.databaseBranchId);
  assert.notEqual(isolated.outputs.appURL, a.outputs.appURL);
  assert.equal((await request(isolated.outputs.appURL)).rows.length, 0);
  assert.equal((await request(a.outputs.appURL)).release, "A");
  scope = primaryScope;
  records();
  evidence.assertions.push(
    "Two simultaneous feature-branch previews kept database, alias and state isolated without new program code",
  );
  const migrationJournal = path.join(app, "migrations/meta/_journal.json");
  const validJournal = fs.readFileSync(migrationJournal);
  const invalid = JSON.parse(validJournal);
  invalid.entries.push({
    idx: 1,
    version: "7",
    when: 1760000001000,
    tag: "0001_invalid",
    breakpoints: true,
  });
  fs.writeFileSync(migrationJournal, JSON.stringify(invalid));
  fs.writeFileSync(path.join(app, "migrations/0001_invalid.sql"), "THIS IS NOT VALID SQL;\n");
  const failedMigration = runRelease("migration-failure");
  assert.notEqual(failedMigration.status, 0);
  assert.equal(failedMigration.run.phase, "migration");
  assert.equal(failedMigration.outputs.deploymentId, a.outputs.deploymentId);
  assert.equal(failedMigration.last.release, "A");
  assert.equal((await request(a.outputs.appURL)).release, "A");
  await sqlAssertions("A");
  fs.writeFileSync(migrationJournal, validJournal);
  fs.rmSync(path.join(app, "migrations/0001_invalid.sql"));
  evidence.assertions.push(
    "Invalid migration stopped before full apply; the A deployment and schema history remained intact",
  );

  fs.writeFileSync(path.join(app, "src/release.ts"), "export const release = 'B'\n");
  fs.writeFileSync(
    path.join(app, "migrations/0001_add_notes.sql"),
    "ALTER TABLE premise_e2e ADD COLUMN notes text;\n",
  );
  const journalFile = path.join(app, "migrations/meta/_journal.json");
  const journal = JSON.parse(fs.readFileSync(journalFile));
  journal.entries.push({
    idx: 1,
    version: "7",
    when: 1760000001000,
    tag: "0001_add_notes",
    breakpoints: true,
  });
  fs.writeFileSync(journalFile, JSON.stringify(journal));
  command("mise", ["run", "build"], app);
  // Erase runner-local records; recovery must reload A from the remote store.
  fs.rmSync(records().directory, { recursive: true, force: true });
  const b = runRelease("B", { PREMISE_E2E_FAIL_HEALTH: "1" });
  assert.notEqual(b.status, 0);
  assert.equal(b.run.phase, "health");
  assert.equal(b.run.recovery_succeeded, true);
  assert.equal(b.last.release, "A", "failed B must never replace last successful A");
  assert.notEqual(b.outputs.deploymentId, a.outputs.deploymentId, "B must actually have deployed");
  const restored = await request(a.outputs.appURL);
  assert.equal(restored.release, "A", "Premise must automatically restore A");
  assert.ok(restored.rows.some((row) => row.id === "sentinel" && row.value === "preserved"));
  await request(a.outputs.appURL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      id: "after-rollback",
      value: "A-compatible-with-B-schema",
    }),
  });
  await sqlAssertions("B");
  const alias = await vercel("GET", `/v4/aliases/${encodeURIComponent(a.outputs.aliasName)}`);
  assert.equal(alias.status, 200);
  const aliasDetails = await alias.json();
  assert.equal(aliasDetails.deployment?.id ?? aliasDetails.deploymentId, a.outputs.deploymentId);
  const adminAlias = await vercel(
    "GET",
    `/v4/aliases/${encodeURIComponent(a.outputs.adminAliasName)}`,
  );
  assert.equal(adminAlias.status, 200);
  const adminDetails = await adminAlias.json();
  assert.equal(
    adminDetails.deployment?.id ?? adminDetails.deploymentId,
    a.outputs.adminDeploymentId,
  );
  evidence.assertions.push(
    "B migrated/deployed, health fault triggered automatic A rollback for both apps, SQL and serving identity verified",
  );

  fs.writeFileSync(path.join(app, "src/release.ts"), "export const release = 'C'\n");
  command("mise", ["run", "build"], app);
  const c = runRelease("C", {
    PREMISE_E2E_FAIL_HEALTH: "1",
    PREMISE_E2E_FAIL_RECOVERY: "1",
  });
  assert.notEqual(c.status, 0);
  assert.equal(c.run.recovery_attempted, true);
  assert.equal(c.run.recovery_succeeded, false, "recovery-hook failure must be visible");
  assert.equal(c.last.release, "A");
  await sqlAssertions("B");
  evidence.assertions.push("recovery-hook failure preserved A record and reported failed recovery");
} catch (error) {
  originalFailure = error;
  throw error;
} finally {
  const cleanupErrors = [];
  const attempt = async (operation) => {
    try {
      await operation();
    } catch (error) {
      cleanupErrors.push(error);
    }
  };
  if (deploymentStarted) {
    for (const cleanupScope of cleanupScopes) {
      scope = cleanupScope;
      await attempt(async () => {
        records();
        const listing = await vercel(
          "GET",
          `/v6/deployments?projectId=${encodeURIComponent(project)}&limit=100`,
        );
        assert.equal(listing.status, 200, "test-owned deployment inventory must be accessible");
        const candidates = (await listing.json()).deployments ?? [];
        for (const candidate of candidates) {
          const detailsResponse = await vercel(
            "GET",
            `/v13/deployments/${candidate.uid ?? candidate.id}`,
          );
          if (detailsResponse.status === 404) continue;
          assert.equal(detailsResponse.status, 200);
          const details = await detailsResponse.json();
          if (details.projectId === project && details.meta?.premiseStack === stack)
            retainedDeployments.add(details.id ?? details.uid);
        }
        const neon = await fetch(
          `https://console.neon.tech/api/v2/projects/${process.env.NEON_PROJECT_ID}/branches`,
          { headers: { Authorization: `Bearer ${process.env.NEON_API_KEY}` } },
        );
        assert.equal(neon.status, 200);
        for (const resource of (await neon.json()).branches ?? []) {
          if (resource.name === `premise-${stack}-db`) branches.add(resource.id);
        }
      });
      await attempt(async () => {
        let current = {};
        try {
          current = publicOutputs();
        } catch {}
        if (current.adminDeploymentId) retainedDeployments.add(current.adminDeploymentId);
        if (current.adminAliasName) aliases.add(current.adminAliasName);
        if (current.deploymentId) retainedDeployments.add(current.deploymentId);
        if (current.databaseBranchId) branches.add(current.databaseBranchId);
        if (current.aliasName) aliases.add(current.aliasName);
        command(pm, ["infra", "destroy", ...scope], workspace, {}, true);
        for (const suffix of ["database-url", "app-marker"]) {
          const check = spawnSync(
            "gcloud",
            [
              "secrets",
              "describe",
              `premise-e2e-${stack}-${suffix}`,
              "--project",
              process.env.GCP_PROJECT_ID,
            ],
            { encoding: "utf8" },
          );
          assert.notEqual(check.status, 0, "preview-owned secret survived cleanup");
          assert.ok(check.stderr.includes("NOT_FOUND"), "could not verify secret cleanup");
        }
      });
    }
    for (const aliasName of aliases) {
      await attempt(async () => {
        const check = await vercel("GET", `/v4/aliases/${encodeURIComponent(aliasName)}`);
        if (check.status === 404) return;
        assert.equal(check.status, 200);
        const details = await check.json();
        assert.equal(details.alias, aliasName);
        const deletion = await vercel("DELETE", `/v2/aliases/${details.uid ?? details.id}`);
        assert.ok(deletion.ok || deletion.status === 404);
        assert.equal(
          (await vercel("GET", `/v4/aliases/${encodeURIComponent(aliasName)}`)).status,
          404,
        );
      });
    }

    for (const id of retainedDeployments) {
      await attempt(async () => {
        const response = await vercel("DELETE", `/v13/deployments/${id}`);
        assert.ok(response.ok || response.status === 404);
        const check = await vercel("GET", `/v13/deployments/${id}`);
        assert.equal(check.status, 404, "test deployment must be gone");
      });
    }
    for (const id of branches) {
      await attempt(async () => {
        const check = await fetch(
          `https://console.neon.tech/api/v2/projects/${process.env.NEON_PROJECT_ID}/branches/${id}`,
          { headers: { Authorization: `Bearer ${process.env.NEON_API_KEY}` } },
        );
        assert.equal(check.status, 404, "preview database branch must be gone");
      });
    }
    if (cleanupErrors.length === 0) {
      for (const cleanupScope of cleanupScopes) {
        scope = cleanupScope;
        await attempt(async () => {
          const state = records();
          for (const filename of ["last-success.json", "last-run.json"]) {
            const local = path.join(state.directory, filename);
            if (!fs.existsSync(local)) continue;
            command("gcloud", ["storage", "cp", local, `${state.archive}/${filename}`], workspace);
            const removal = spawnSync("gcloud", ["storage", "rm", `${state.remote}/${filename}`], {
              encoding: "utf8",
            });
            if (removal.status !== 0 && !/matched no objects|No URLs matched/i.test(removal.stderr))
              throw new Error("Could not clear active test-preview recovery records");
          }
        });
      }
    }
    if (cleanupErrors.length === 0)
      evidence.assertions.push(
        "Exact test-owned Neon branches, aliases and retained Vercel deployments removed; recovery records archived and active references cleared; containers preserved",
      );
  }
  evidence.cleanup_succeeded = cleanupErrors.length === 0;
  evidence.succeeded = !originalFailure && cleanupErrors.length === 0;
  fs.mkdirSync(path.join(workspace, "test-results"), { recursive: true });
  fs.writeFileSync(
    path.join(workspace, "test-results/rollback.json"),
    JSON.stringify(evidence, null, 2),
  );
  if (cleanupErrors.length)
    throw new AggregateError(
      originalFailure ? [originalFailure, ...cleanupErrors] : cleanupErrors,
      "Live lifecycle/cleanup failed; inspect exact test-owned resources",
    );
}
