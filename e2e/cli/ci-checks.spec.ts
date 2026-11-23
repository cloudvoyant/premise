import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { usePremiseHarness } from "../common/fixtures.js";
import { TESTDATA_ROOT, type PremiseHarness } from "../common/premise-harness.js";
import { copyScaffold, pathExists } from "../common/test-utils.js";

function hostCIPlatform(): string {
  if (process.platform === "darwin") return "macos";
  if (process.platform === "win32") return "windows";
  return "linux";
}

async function setupCIWorkspace(h: PremiseHarness): Promise<void> {
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

describe("CI checks and staging", () => {
  const harness = usePremiseHarness({ env: { PM_RELEASE: "A" } });

  describe("given a git workspace at v1.0.0 on a feature branch", () => {
    it(
      "when checks pass on a [publish-rc] commit, then tasks run in order, migrations schedule, and RC artifacts stage locally",
      { tags: ["e2e", "cli", "smoke", "ci", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await setupCIWorkspace(h);
        await h.git(["switch", "-c", "feature/example"]);
        await commitChange(h, "feat: add static page [publish-rc]");

        const versionRes = await h.pm(["version", "rc", "--identifier", "1"]);
        const version = versionRes.stdout.trim();
        const artifactsDir = join(h.outputDir, "artifacts-rc");
        const eventsFile = join(h.outputDir, "events.log");
        await mkdir(artifactsDir, { recursive: true });
        const platform = hostCIPlatform();

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
            platform,
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

        // Assert task order, staged artifacts, and migration scheduling
        expect(await readFile(eventsFile, "utf8")).toBe(
          "build::A\ntest::A\nformat:check::A\nlint::A\nrelease:build::A\n",
        );
        expect(await readFile(join(artifactsDir, "index.html"), "utf8")).toContain("Release A");
        expect(await readFile(join(artifactsDir, "version.txt"), "utf8")).toBe("rc:1.1.0-rc.1\n");

        const plan = await h.pm(["ci", "plan", "--ci-output"]);
        expect(plan.stdout).toContain("has_migrations=true\n");
        expect(plan.stdout).toContain("migration_matrix=");
        await h.writeManifest();
      },
    );

    it(
      "when a check task fails, then execution halts before release:build with no staged artifacts",
      { tags: ["e2e", "cli", "ci", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await setupCIWorkspace(h);
        await h.git(["switch", "-c", "feature/failing-check"]);
        await commitChange(h, "feat: broken test [publish-rc]");
        const version = (await h.pm(["version", "rc", "--identifier", "1"])).stdout.trim();
        const failedArtifacts = join(h.outputDir, "artifacts-failed-check");
        await mkdir(failedArtifacts, { recursive: true });
        const platform = hostCIPlatform();

        // Act
        const failure = await h.pmFail(
          [
            "ci",
            "flow",
            "on-commit",
            "--release",
            "none",
            "--project",
            "web",
            "--platform",
            platform,
            "--channel",
            "rc",
            "--version",
            version,
            "--output-dir",
            failedArtifacts,
          ],
          {
            env: {
              PREMISE_TEST_FAIL_TASK: "test",
              PREMISE_ARTIFACT_DIR: failedArtifacts,
              PREMISE_RELEASE_CHANNEL: "rc",
              RELEASE_VERSION: version.replace(/^v/, ""),
            },
          },
        );

        // Assert
        expect(failure.code).not.toBe(0);
        expect(failure.combined).toContain("requested test failure");
        expect(await pathExists(join(failedArtifacts, "index.html"))).toBe(false);
        await h.writeManifest();
      },
    );

    it(
      "when release:build fails, then execution fails without creating git tags",
      { tags: ["e2e", "cli", "ci", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await setupCIWorkspace(h);
        await h.git(["switch", "-c", "feature/failing-stage"]);
        await commitChange(h, "feat: broken stage [publish-rc]");
        const version = (await h.pm(["version", "rc", "--identifier", "1"])).stdout.trim();
        const failedArtifacts = join(h.outputDir, "artifacts-failed-stage");
        await mkdir(failedArtifacts, { recursive: true });
        const platform = hostCIPlatform();

        // Act
        const failure = await h.pmFail(
          [
            "ci",
            "flow",
            "on-commit",
            "--release",
            "none",
            "--project",
            "web",
            "--platform",
            platform,
            "--channel",
            "rc",
            "--version",
            version,
            "--output-dir",
            failedArtifacts,
          ],
          {
            env: {
              PREMISE_TEST_FAIL_TASK: "release:build",
              PREMISE_ARTIFACT_DIR: failedArtifacts,
              PREMISE_RELEASE_CHANNEL: "rc",
              RELEASE_VERSION: version.replace(/^v/, ""),
            },
          },
        );

        // Assert
        expect(failure.code).not.toBe(0);
        expect(failure.combined).toContain("stage native artifacts");
        const tags = (await h.git(["tag", "--list"])).stdout.trim();
        expect(tags).toBe("v1.0.0");
        await h.writeManifest();
      },
    );

    it(
      "when merging to main, then conventional commits bump to v1.1.0 and stage stable artifacts",
      { tags: ["e2e", "cli", "ci", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await setupCIWorkspace(h);
        await commitChange(h, "feat: add stable page");
        const version = (await h.pm(["version", "next"])).stdout.trim();
        const artifactsDir = join(h.outputDir, "artifacts-stable");
        await mkdir(artifactsDir, { recursive: true });
        const platform = hostCIPlatform();

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
            platform,
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
  });

  describe("given a multi-platform workspace (linux, macos, windows)", () => {
    it.skip(
      "when planning CI, then mode=matrix emits per-platform rows and generated workflows match canonical assets [CLO-236]",
      { tags: ["e2e", "cli", "ci", "known-bug", "replay"] },
      async () => {
        // Tracked by CLO-236: template registries have workflow & action-version drift.
        const h = harness();
        await setupCIWorkspace(h);
        const manifestPath = join(h.workspace, "premise.yaml");
        const updated = (await readFile(manifestPath, "utf8")).replace(
          /release_platforms:[\s\S]*?(?=\n\w|\n\s{2,4}\w+:|$)/,
          "release_platforms:\n          - linux\n          - macos\n          - windows",
        );
        await writeFile(manifestPath, updated, "utf8");

        const plan = await h.pm(["ci", "plan", "--ci-output"]);

        expect(plan.stdout).toContain("mode=matrix\n");
        expect(plan.stdout).toContain("native=true\n");
        await h.writeManifest();
      },
    );
  });
});
