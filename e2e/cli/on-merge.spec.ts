/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag on-merge
 */
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
import { copyScaffold, isLiveOrRecordMode } from "../common/test-utils.js";

// Fixtures -------------------------------------------------------------------

const ECOSYSTEM_FIXTURES = [
  { label: "bun", fixture: "workspaces/bun", tag: "bun" },
  { label: "rust", fixture: "workspaces/cargo", tag: "rust" },
  { label: "go", fixture: "workspaces/go", tag: "go" },
] as const;

// Tests ----------------------------------------------------------------------

describe("premise on-merge", { tags: ["e2e", "cli", "ci", "on-merge"] }, () => {
  const harness = usePremiseHarness({ env: { PM_RELEASE: "A" } });

  describe("Given a project with infra and generic apps and libs", () => {
    beforeEach(async () => {
      await setupGenericInfraWorkspace(harness());
    });

    it(
      "It bumps the version from conventional commits and stages stable release artifacts",
      { tags: ["smoke", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await commitChange(h, "feat: add stable page");
        const version = (await h.pm(["version", "next"])).stdout.trim();
        const artifactsDir = join(h.outputDir, "artifacts-stable");
        await mkdir(artifactsDir, { recursive: true });

        // Act
        await h.pm(
          [
            "ci",
            "flow",
            "on-merge",
            "--release",
            "none",
            "--project",
            "web",
            "--platform",
            hostCIPlatform(),
            "--channel",
            "stable",
            "--version",
            version,
            "--output-dir",
            artifactsDir,
          ],
          {
            env: {
              PREMISE_ARTIFACT_DIR: artifactsDir,
              PREMISE_RELEASE_CHANNEL: "stable",
              RELEASE_VERSION: version.replace(/^v/, ""),
            },
          },
        );

        // Assert
        expect(version).toBe("v1.1.0");
        expect(await readFile(join(artifactsDir, "index.html"), "utf8")).toContain("Release A");
        expect(await readFile(join(artifactsDir, "version.txt"), "utf8")).toBe("stable:1.1.0\n");
        await h.writeManifest();
      },
    );

    it.skipIf(!isLiveOrRecordMode())(
      "It destroys the merged preview environment idempotently and updates dev in-place to release B",
      { tags: ["gcp", "pulumi", "live"] },
      async () => {
        // Arrange
        const h = harness();
        const ctx = await configureLiveMergeBackends(h);
        await h.pm(
          ["ci", "flow", "on-commit", "--environment", "preview", "--branch", ctx.branch],
          {
            env: { PM_RELEASE: "A" },
          },
        );
        const previewBucket = (
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
        await h.pm(["ci", "flow", "on-deploy", "--environment", "dev"], {
          env: { PM_RELEASE: "A" },
        });
        const devBucket = (
          await h.pm(["infra", "query", "--env", "dev", "--output", "bucketName"])
        ).stdout.trim();
        ctx.devBucket = devBucket;

        // Act
        await h.pm(["ci", "flow", "on-merge", "--environment", "preview", "--branch", ctx.branch], {
          env: { PM_RELEASE: "A" },
        });
        await h.pm(["ci", "flow", "on-merge", "--environment", "dev"], {
          env: { PM_RELEASE: "B" },
        });

        // Assert preview destroyed (idempotent) and dev updated in-place to B
        await assertGcsBucketDeleted(previewBucket, h.workspace);
        await h.pm(["ci", "flow", "on-merge", "--environment", "preview", "--branch", ctx.branch], {
          env: { PM_RELEASE: "A" },
        });
        expect(
          (await h.pm(["infra", "query", "--env", "dev", "--output", "bucketName"])).stdout.trim(),
        ).toBe(devBucket);
        expect(
          (
            await h.run("gcloud", ["storage", "cat", `gs://${devBucket}/schema-version.txt`], {
              expectSuccess: true,
            })
          ).stdout.trim(),
        ).toBe("dev:B");
        expect(
          (
            await h.run("gcloud", ["storage", "cat", `gs://${devBucket}/index.html`], {
              expectSuccess: true,
            })
          ).stdout,
        ).toContain("<h1>Release B</h1>");
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

    it("It bumps version and runs on-merge checks without scheduling infrastructure", async () => {
      // Arrange
      const h = harness();
      await commitChange(h, "fix: patch generic app");
      const eventsFile = join(h.outputDir, "events.log");

      // Act
      const nextVersion = (await h.pm(["version", "next"])).stdout.trim();
      await h.pm(["ci", "flow", "on-merge", "--release", "none"], {
        env: { PREMISE_TEST_EVENTS: eventsFile },
      });

      // Assert
      expect(nextVersion).toBe("v1.0.1");
      const events = await readFile(eventsFile, "utf8");
      expect(events).toContain("shared:build::A");
      expect(events).toContain("web:lint::A");
      await h.writeManifest();
    });
  });

  for (const eco of ECOSYSTEM_FIXTURES) {
    describe(`Given a project with ${eco.label} libs`, { tags: [eco.tag] }, () => {
      beforeEach(async () => {
        const h = harness();
        await copyScaffold(join(TESTDATA_ROOT, eco.fixture), h.workspace);
        await h.initGit(`chore: initial ${eco.label} project`, "v1.0.0");
      });

      it(
        `It handles multiple platforms, nobuild, and local-only ${eco.label} variants on merge`,
        { tags: ["replay"] },
        async () => {
          // Arrange
          const h = harness();
          await commitChange(h, `feat: add ${eco.label} feature`);

          // Act
          const version = (await h.pm(["version", "next"])).stdout.trim();
          const plan = await h.pm(["ci", "plan", "--ci-output"]);
          const flow = await h.pm(["ci", "flow", "on-merge", "--release", "none"]);

          // Assert
          expect(version).toBe("v1.1.0");
          expect(plan.stdout).toContain("mode=matrix\n");
          expect(flow.combined).toContain("nobuild-lib:skip-build");
          expect(flow.combined).toContain("local-lib:test");
          await h.writeManifest();
        },
      );

      it.skip(
        `It handles public ${eco.label} packages on merge [CLO-228..CLO-234]`,
        { tags: ["known-bug", "live"] },
        async () => {
          const h = harness();
          await h.pm(["ci", "plan", "--ci-output"]);
          await h.writeManifest();
        },
      );

      it.skip(
        `It handles private ${eco.label} packages for Artifact Registry / alternate registries on merge [CLO-229, CLO-232..CLO-234]`,
        { tags: ["known-bug", "live"] },
        async () => {
          const h = harness();
          await h.pm(["ci", "plan", "--ci-output"]);
          await h.writeManifest();
        },
      );

      it.skip(
        `It handles stable GitHub Releases for ${eco.label} on merge [CLO-230, CLO-235, CLO-236]`,
        { tags: ["known-bug", "live"] },
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

async function configureLiveMergeBackends(h: PremiseHarness) {
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

  const state = { branch, gcpProject, devBucket: "" };
  h.registerRemoteCleanup(async () => {
    await h.pmFail(["ci", "flow", "on-merge", "--environment", "preview", "--branch", branch]);
    await destroyPulumiStack({
      infraDir: join(h.workspace, "infra"),
      stack: "dev",
      backendUrl: runBackend,
      env: h.env,
    }).catch(() => {});
    if (state.devBucket) {
      await assertGcsBucketDeleted(state.devBucket, h.workspace);
    }
  });
  return state;
}
