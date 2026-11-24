/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag on-commit
 */
import { createHash } from "node:crypto";
import { chmod, mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { usePremiseHarness } from "../common/fixtures.js";
import {
  assertGcsBucketDeleted,
  destroyPulumiStack,
  patchManifestInfra,
  resolveLiveGcpConfig,
} from "../common/gcp-driver.js";
import { TESTDATA_ROOT, type PremiseHarness } from "../common/premise-harness.js";
import { copyScaffold, isLiveOrRecordMode, pathExists } from "../common/test-utils.js";

// Fixtures -------------------------------------------------------------------

const ECOSYSTEM_FIXTURES = [
  { label: "bun", fixture: "workspaces/bun", tag: "bun" },
  { label: "rust", fixture: "workspaces/cargo", tag: "rust" },
  { label: "go", fixture: "workspaces/go", tag: "go" },
] as const;

// Tests ----------------------------------------------------------------------

describe("premise on-commit", { tags: ["e2e", "cli", "ci", "on-commit"] }, () => {
  const harness = usePremiseHarness({ env: { PM_RELEASE: "A" } });

  describe("Given a project with infra and generic apps and libs", () => {
    beforeEach(async () => {
      await setupGenericInfraWorkspace(harness());
    });

    it(
      "It runs checks in order, schedules migrations, and stages RC artifacts on [publish-rc]",
      { tags: ["smoke", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await h.git(["switch", "-c", "feature/example"]);
        await commitChange(h, "feat: add static page [publish-rc]");
        const version = (await h.pm(["version", "rc", "--identifier", "1"])).stdout.trim();
        const artifactsDir = join(h.outputDir, "artifacts-rc");
        const eventsFile = join(h.outputDir, "events.log");
        await mkdir(artifactsDir, { recursive: true });

        // Act
        await h.pm(
          [
            "ci",
            "flow",
            "on-commit",
            "--release",
            "none",
            "--project",
            "web",
            "--platform",
            hostCIPlatform(),
            "--channel",
            "rc",
            "--version",
            version,
            "--output-dir",
            artifactsDir,
          ],
          {
            env: {
              GITHUB_EVENT_NAME: "push",
              GITHUB_REF: "refs/heads/feature/example",
              PREMISE_TEST_EVENTS: eventsFile,
              PREMISE_ARTIFACT_DIR: artifactsDir,
              PREMISE_RELEASE_CHANNEL: "rc",
              RELEASE_VERSION: version.replace(/^v/, ""),
            },
          },
        );

        // Assert
        expect(await readFile(eventsFile, "utf8")).toBe(
          "build::A\ntest::A\nformat:check::A\nlint::A\nrelease:build::A\n",
        );
        expect(await readFile(join(artifactsDir, "index.html"), "utf8")).toContain("Release A");
        expect(await readFile(join(artifactsDir, "version.txt"), "utf8")).toBe("rc:1.1.0-rc.1\n");
        const plan = await h.pm(["ci", "plan", "--ci-output"]);
        expect(plan.stdout).toContain("has_infra=true\n");
        expect(plan.stdout).toContain("has_migrations=true\n");
        expect(plan.stdout).toContain("migration_matrix=");
        await h.writeManifest();
      },
    );

    it(
      "It halts before release:build when a check fails and creates no tags when release:build fails",
      { tags: ["replay"] },
      async () => {
        // Arrange
        const h = harness();
        await h.git(["switch", "-c", "feature/failing-gates"]);
        await commitChange(h, "feat: broken commit [publish-rc]");
        const version = (await h.pm(["version", "rc", "--identifier", "1"])).stdout.trim();
        const failedCheckDir = join(h.outputDir, "artifacts-failed-check");
        const failedStageDir = join(h.outputDir, "artifacts-failed-stage");
        await mkdir(failedCheckDir, { recursive: true });
        await mkdir(failedStageDir, { recursive: true });

        // Act: check task fails
        const checkFail = await h.pmFail(
          [
            "ci",
            "flow",
            "on-commit",
            "--release",
            "none",
            "--project",
            "web",
            "--platform",
            hostCIPlatform(),
            "--channel",
            "rc",
            "--version",
            version,
            "--output-dir",
            failedCheckDir,
          ],
          { env: { PREMISE_TEST_FAIL_TASK: "test", PREMISE_ARTIFACT_DIR: failedCheckDir } },
        );

        // Act: release:build fails
        const stageFail = await h.pmFail(
          [
            "ci",
            "flow",
            "on-commit",
            "--release",
            "none",
            "--project",
            "web",
            "--platform",
            hostCIPlatform(),
            "--channel",
            "rc",
            "--version",
            version,
            "--output-dir",
            failedStageDir,
          ],
          {
            env: { PREMISE_TEST_FAIL_TASK: "release:build", PREMISE_ARTIFACT_DIR: failedStageDir },
          },
        );

        // Assert
        expect(checkFail.code).not.toBe(0);
        expect(checkFail.combined).toContain("requested test failure");
        expect(await pathExists(join(failedCheckDir, "index.html"))).toBe(false);
        expect(stageFail.code).not.toBe(0);
        expect(stageFail.combined).toContain("stage native artifacts");
        expect((await h.git(["tag", "--list"])).stdout.trim()).toBe("v1.0.0");
        await h.writeManifest();
      },
    );

    it.skipIf(!isLiveOrRecordMode())(
      "It provisions a preview environment, runs migrations and app deploy, combines secrets with Pulumi outputs, and rolls back on health failure",
      { tags: ["gcp", "pulumi", "fnox", "live"] },
      async () => {
        // Arrange
        const h = harness();
        const ctx = await configureLivePreviewBackend(h);
        await h.git(["switch", "-c", ctx.branch]);

        // Act: deploy Release A to preview
        await h.pm(
          ["ci", "flow", "on-commit", "--environment", "preview", "--branch", ctx.branch],
          {
            env: { PM_RELEASE: "A" },
          },
        );
        const bucket = (
          await h.pm([
            "infra",
            "query",
            "--env",
            "preview",
            "--branch",
            ctx.branch,
            "--output",
            "bucketName",
          ])
        ).stdout.trim();
        ctx.bucket = bucket;

        // Assert Release A, Migration A, saved backend, and secrets + Pulumi output combination
        expect(await readGcs(h, bucket, "index.html")).toContain("<h1>Release A</h1>");
        expect((await readGcs(h, bucket, "schema-version.txt")).trim()).toBe("preview:A");
        await assertPreviewBackendState(h, ctx.alternateBackend, ctx.branch);
        await h.pm([
          "secrets",
          "pull",
          "--project",
          "web",
          "--env",
          "preview",
          "--branch",
          ctx.branch,
        ]);
        const envLocal = await h.readFile("apps/web/.env.local");
        expect(envLocal).toContain('APP_MESSAGE="preview-example"');
        expect(envLocal).toContain(`BUCKET_NAME="${bucket}"`);

        // Act: deploy Release B with failing health check
        const failB = await h.pmFail(
          ["ci", "flow", "on-deploy", "--environment", "preview", "--branch", ctx.branch],
          { env: { PM_RELEASE: "B", PREMISE_TEST_FAIL_HEALTH: "preview" } },
        );

        // Assert rollback restores Release A while keeping Migration B
        expect(failB.code).not.toBe(0);
        expect(failB.combined).toContain("health");
        expect(await readGcs(h, bucket, "index.html")).toContain("<h1>Release A</h1>");
        expect((await readGcs(h, bucket, "schema-version.txt")).trim()).toBe("preview:B");
        h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
        await h.writeManifest();
      },
    );
  });

  describe("Given a project without infra and generic apps and libs", { tags: ["replay"] }, () => {
    beforeEach(async () => {
      const h = harness();
      await copyScaffold(join(TESTDATA_ROOT, "workspaces/generic-no-infra"), h.workspace);
      await h.initGit("chore: initial generic project", "v1.0.0");
    });

    it("It plans has_infra=false and runs build, test, format:check, and lint across generic libs and apps", async () => {
      // Arrange
      const h = harness();
      const eventsFile = join(h.outputDir, "events.log");

      // Act
      const plan = await h.pm(["ci", "plan", "--ci-output"]);
      await h.pm(["ci", "flow", "on-commit", "--release", "none"], {
        env: { PREMISE_TEST_EVENTS: eventsFile },
      });

      // Assert
      expect(plan.stdout).toContain("has_infra=false\n");
      const events = await readFile(eventsFile, "utf8");
      expect(events).toContain("shared:build::A");
      expect(events).toContain("shared:test::A");
      expect(events).toContain("web:build::A");
      expect(events).toContain("web:test::A");
      expect(events).toContain("web:lint::A");
      await h.writeManifest();
    });
  });

  for (const eco of ECOSYSTEM_FIXTURES) {
    describe(`Given a project with ${eco.label} libs`, { tags: [eco.tag, "replay"] }, () => {
      beforeEach(async () => {
        const h = harness();
        await copyScaffold(join(TESTDATA_ROOT, eco.fixture), h.workspace);
        await h.initGit(`chore: initial ${eco.label} project`, "v1.0.0");
      });

      it(`It handles multiple platforms in CI planning (linux, macos, windows)`, async () => {
        // Arrange
        const h = harness();

        // Act
        const plan = await h.pm(["ci", "plan", "--ci-output"]);

        // Assert
        expect(plan.stdout).toContain("has_infra=false\n");
        expect(plan.stdout).toContain("mode=matrix\n");
        expect(plan.stdout).toContain("native=true\n");
        expect(plan.stdout).toContain('"platform":"linux"');
        expect(plan.stdout).toContain('"platform":"macos"');
        expect(plan.stdout).toContain('"platform":"windows"');
        await h.writeManifest();
      });

      it(`It handles nobuild and local-only ${eco.label} projects during on-commit checks`, async () => {
        // Arrange
        const h = harness();

        // Act
        const result = await h.pm(["ci", "flow", "on-commit", "--release", "none"]);

        // Assert
        expect(result.combined).toContain("nobuild-lib:skip-build");
        expect(result.combined).toContain("local-lib:test");
        expect(result.combined).toContain("public-lib:test");
        expect(result.combined).toContain("private-lib:test");
        await h.writeManifest();
      });

      it.skip(
        `It handles public packages for the ${eco.label} registry [CLO-228..CLO-234]`,
        { tags: ["known-bug"] },
        async () => {
          const h = harness();
          await h.pm(["ci", "plan", "--ci-output"]);
          await h.writeManifest();
        },
      );

      it.skip(
        `It handles private packages for Artifact Registry / alternate ${eco.label} registries [CLO-229, CLO-232..CLO-234]`,
        { tags: ["known-bug"] },
        async () => {
          const h = harness();
          await h.pm(["ci", "plan", "--ci-output"]);
          await h.writeManifest();
        },
      );

      it.skip(
        `It handles RC binary release staging and workflow parity for ${eco.label} [CLO-230, CLO-235, CLO-236]`,
        { tags: ["known-bug"] },
        async () => {
          const h = harness();
          await h.pm(["ci", "plan", "--ci-output"]);
          await h.writeManifest();
        },
      );
    });
  }
});

// Private helpers ------------------------------------------------------------

function hostCIPlatform(): string {
  if (process.platform === "darwin") return "macos";
  if (process.platform === "win32") return "windows";
  return "linux";
}

async function setupGenericInfraWorkspace(h: PremiseHarness): Promise<void> {
  await copyScaffold(join(TESTDATA_ROOT, "pulumi/client"), h.workspace);
  await h.writeFile(
    "mise.toml",
    "monorepo_root = true\n[monorepo]\nconfig_roots = ['apps/*', 'libs/*']\n",
  );
  const manifestPath = join(h.workspace, "premise.yaml");
  const manifest = await readFile(manifestPath, "utf8");
  const platform = hostCIPlatform();
  const updated = manifest.includes("release_platforms:")
    ? manifest.replace(/release_platforms:.*$/m, `release_platforms: [${platform}]`)
    : manifest.replace(
        "path: apps/web",
        `path: apps/web\n      ci:\n        release_platforms:\n          - ${platform}`,
      );
  await writeFile(manifestPath, updated, "utf8");
  await h.initGit("chore: initial project", "v1.0.0");
}

async function commitChange(h: PremiseHarness, message: string): Promise<void> {
  await h.writeFile("CHANGE.txt", `${message}\n`);
  await h.git(["add", "CHANGE.txt"]);
  await h.git(["commit", "--quiet", "-m", message]);
}

async function readGcs(h: PremiseHarness, bucket: string, object: string): Promise<string> {
  return (
    await h.run("gcloud", ["storage", "cat", `gs://${bucket}/${object}`], { expectSuccess: true })
  ).stdout;
}

async function assertPreviewBackendState(
  h: PremiseHarness,
  backendUrl: string,
  branch: string,
): Promise<void> {
  const stack = `preview-${branch.replace(/^feature\//, "")}-${createHash("sha256").update(branch).digest("hex").slice(0, 10)}`;
  const statePath = `${backendUrl}/.pulumi/stacks/premise-gcs-fixture/${stack}.json`;
  await h.run("gcloud", ["storage", "objects", "describe", statePath, "--format=value(name)"], {
    expectSuccess: true,
  });
}

async function configureLivePreviewBackend(h: PremiseHarness) {
  const { project: gcpProject, backendUrl: baseBackend } = resolveLiveGcpConfig();
  const runBackend = `${baseBackend.replace(/\/+$/, "")}/run-${h.runId}`;
  const alternateBackend = `${runBackend}/alternate`;
  const branch = `feature/static-${h.runId}`;

  const shimBin = join(h.outputDir, "pm-bin");
  await mkdir(shimBin, { recursive: true });
  await writeFile(join(shimBin, "pm"), `#!/bin/sh\nexec "${h.binary}" "$@"\n`, { mode: 0o755 });
  await chmod(join(shimBin, "pm"), 0o755);
  h.env.PATH = `${shimBin}:${h.env.PATH ?? process.env.PATH ?? ""}`;
  h.env.PULUMI_CONFIG_PASSPHRASE = "";
  h.env.PM_RELEASE = "A";
  h.env.PM_BRANCH = "";

  const manifestPath = join(h.workspace, "premise.yaml");
  const original = await readFile(manifestPath, "utf8");
  const updated = patchManifestInfra(original, {
    primaryBackend: runBackend,
    alternateBackend,
    gcpProject,
  });
  await writeFile(manifestPath, updated, "utf8");

  const state = { branch, alternateBackend, gcpProject, bucket: "" };
  h.registerRemoteCleanup(async () => {
    await h.pmFail(["ci", "flow", "on-merge", "--environment", "preview", "--branch", branch]);
    if (state.bucket) {
      await assertGcsBucketDeleted(state.bucket, h.workspace);
    }
    await destroyPulumiStack({
      infraDir: join(h.workspace, "infra/env"),
      stack: "dev",
      backendUrl: runBackend,
      env: h.env,
    }).catch(() => {});
  });
  return state;
}
