/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag on-merge
 */
import { mkdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { beforeAll, describe, expect, it } from "vitest";
import {
  commitChange,
  configureLiveInfraWorkspace,
  copyScaffold,
  setupGenericInfraWorkspace,
  useSuiteHarness,
  type LiveInfraContext,
} from "../../libs/testutils/fixtures.js";
import { isLiveOrRecordMode } from "../../libs/testutils/mode.js";
import { REPO_ROOT, TESTDATA_ROOT } from "../../libs/testutils/premise-harness.js";
import { hostCIPlatform } from "../../libs/tsutils/fs.js";
import { assertGcsBucketDeleted, readGcsObject } from "../../libs/tsutils/gcsx.js";

// Fixtures -------------------------------------------------------------------

const ECOSYSTEM_FIXTURES = [
  { label: "bun", fixture: "workspaces/bun", tag: "bun" },
  { label: "rust", fixture: "workspaces/cargo", tag: "rust" },
  { label: "go", fixture: "workspaces/go", tag: "go" },
] as const;

// Tests ----------------------------------------------------------------------

describe("premise on-merge", { tags: ["e2e", "cli", "ci", "on-merge"] }, () => {
  describe("Given a project with infra and generic apps and libs", () => {
    describe("When merging a feature commit to main", { tags: ["smoke", "replay"] }, () => {
      const suite = useSuiteHarness("on_merge_generic_infra_stage", import.meta.filename, {
        env: { PM_RELEASE: "A" },
      });
      let version = "";
      let artifactsDir = "";

      beforeAll(async () => {
        const h = suite();
        await setupGenericInfraWorkspace(h);
        await commitChange(h, "feat: add stable page");
        version = (await h.pm(["version", "next"])).stdout.trim();
        artifactsDir = join(h.outputDir, "artifacts-stable");
        await mkdir(artifactsDir, { recursive: true });

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
        await h.writeManifest();
      });

      it("It should bump the version from conventional commits", () => {
        expect(version).toBe("v1.1.0");
      });

      it("It should stage stable release artifacts", async () => {
        expect(await readFile(join(artifactsDir, "index.html"), "utf8")).toContain("Release A");
        expect(await readFile(join(artifactsDir, "version.txt"), "utf8")).toBe("stable:1.1.0\n");
      });
    });

    describe(
      "When updating dev and cleaning up preview on merge",
      { tags: ["gcp", "pulumi", "live"] },
      () => {
        const suite = useSuiteHarness("on_merge_live_dev_and_preview", import.meta.filename, {
          env: { PM_RELEASE: "A" },
          enabled: isLiveOrRecordMode(),
        });
        let ctx: LiveInfraContext;
        let previewBucket = "";
        let devBucket = "";
        let sharedBucket = "";

        beforeAll(async () => {
          if (!isLiveOrRecordMode()) return;
          const h = suite();
          ctx = await configureLiveInfraWorkspace(h, { copyFixture: true, initGit: true });
          ctx.touched.add("preview");
          ctx.touched.add("dev");
          ctx.touched.add("shared");

          // Seed preview A and initial dev A via their own lifecycle flows
          await h.pm(
            ["ci", "flow", "on-commit", "--environment", "preview", "--branch", ctx.branch],
            {
              env: { PM_RELEASE: "A" },
            },
          );
          previewBucket = (
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
          await h.pm(["ci", "flow", "on-merge", "--environment", "dev"], {
            env: { PM_RELEASE: "A" },
          });
          devBucket = (
            await h.pm(["infra", "query", "--env", "dev", "--output", "bucketName"])
          ).stdout.trim();
          ctx.buckets.set("dev", devBucket);

          // Act: merge release B into dev and destroy the merged preview
          await h.pm(["ci", "flow", "on-merge", "--environment", "dev"], {
            env: { PM_RELEASE: "B" },
          });
          sharedBucket = (
            await h.pm(["infra", "query", "--env", "shared", "--output", "bucketName"])
          ).stdout.trim();
          ctx.buckets.set("shared", sharedBucket);
          await h.pm(
            ["ci", "flow", "on-merge", "--environment", "preview", "--branch", ctx.branch],
            {
              env: { PM_RELEASE: "A" },
            },
          );
          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        });

        it.skipIf(!isLiveOrRecordMode())(
          "It should destroy the merged preview idempotently while preserving shared",
          async () => {
            const h = suite();
            await assertGcsBucketDeleted(previewBucket, h.workspace);
            await h.pm(
              ["ci", "flow", "on-merge", "--environment", "preview", "--branch", ctx.branch],
              {
                env: { PM_RELEASE: "A" },
              },
            );
            expect(
              (
                await h.pm(["infra", "query", "--env", "shared", "--output", "bucketName"])
              ).stdout.trim(),
            ).toBe(sharedBucket);
          },
        );

        it.skipIf(!isLiveOrRecordMode())(
          "It should update existing dev in-place to Release B and Migration B",
          async () => {
            const h = suite();
            expect(
              (
                await h.pm(["infra", "query", "--env", "dev", "--output", "bucketName"])
              ).stdout.trim(),
            ).toBe(devBucket);
            expect((await readGcsObject(devBucket, "schema-version.txt", h.workspace)).trim()).toBe(
              "dev:B",
            );
            expect(await readGcsObject(devBucket, "index.html", h.workspace)).toContain(
              "<h1>Release B</h1>",
            );
          },
        );
      },
    );
  });

  describe("Given a project without infra and generic apps and libs", { tags: ["replay"] }, () => {
    const suite = useSuiteHarness("on_merge_generic_no_infra", import.meta.filename, {
      env: { PM_RELEASE: "A" },
    });
    let nextVersion = "";
    let events = "";

    beforeAll(async () => {
      const h = suite();
      await copyScaffold(join(TESTDATA_ROOT, "workspaces/generic-no-infra"), h.workspace);
      await h.initGit("chore: initial generic project", "v1.0.0");
      await commitChange(h, "fix: patch generic app");
      const eventsFile = join(h.outputDir, "events.log");
      nextVersion = (await h.pm(["version", "next"])).stdout.trim();
      await h.pm(["ci", "flow", "on-merge", "--release", "none"], {
        env: { PREMISE_TEST_EVENTS: eventsFile },
      });
      events = await readFile(eventsFile, "utf8");
      await h.writeManifest();
    });

    it("It should compute the next patch version", () => {
      expect(nextVersion).toBe("v1.0.1");
    });

    it("It should run checks across generic libs and apps without scheduling infra", () => {
      expect(events).toContain("shared:build::A");
      expect(events).toContain("web:lint::A");
    });
  });

  for (const eco of ECOSYSTEM_FIXTURES) {
    describe(`Given a project with ${eco.label} libs`, { tags: [eco.tag] }, () => {
      const suite = useSuiteHarness(`on_merge_${eco.label}`, import.meta.filename, {
        env: { PM_RELEASE: "A" },
      });
      let version = "";
      let planOutput = "";
      let flowOutput = "";

      beforeAll(async () => {
        const h = suite();
        await copyScaffold(join(TESTDATA_ROOT, eco.fixture), h.workspace);
        await h.initGit(`chore: initial ${eco.label} project`, "v1.0.0");
        await commitChange(h, `feat: add ${eco.label} feature`);
        version = (await h.pm(["version", "next"])).stdout.trim();
        planOutput = (await h.pm(["ci", "plan", "--ci-output"])).stdout;
        flowOutput = (await h.pm(["ci", "flow", "on-merge", "--release", "none"])).combined;
        await h.writeManifest();
      });

      it(
        `It should handle multiple platforms, nobuild, and local-only ${eco.label} variants on merge`,
        { tags: ["replay"] },
        () => {
          expect(version).toBe("v1.1.0");
          expect(planOutput).toContain("mode=matrix\n");
          expect(flowOutput).toContain("nobuild-lib:skip-build");
          expect(flowOutput).toContain("local-lib:test");
        },
      );

      it(
        `It should handle public ${eco.label} packages on merge [CLO-228..CLO-234]`,
        { tags: ["replay"] },
        () => {
          expect(planOutput).toContain("has_registry_packages=true\n");
          expect(planOutput).toContain("has_github_release=true\n");
        },
      );

      it(
        `It should handle private ${eco.label} packages for Artifact Registry / alternate registries on merge [CLO-229, CLO-232..CLO-234]`,
        { tags: ["replay"] },
        async () => {
          const workflow = await readFile(
            join(REPO_ROOT, "apps/premise/assets/github-workflows/on-merge.yml"),
            "utf8",
          );
          expect(workflow).toContain("NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}");
          expect(workflow).not.toContain("CRATES_TOKEN is unavailable before release");
          expect(workflow).not.toContain("install-premise: build");
        },
      );

      it(
        `It should handle stable GitHub Releases for ${eco.label} on merge [CLO-230, CLO-235, CLO-236]`,
        { tags: ["replay"] },
        async () => {
          const h = suite();
          const stagedDir = join(h.outputDir, `staged-stable-${eco.label}`);
          await mkdir(stagedDir, { recursive: true });
          await h.pm([
            "ci",
            "flow",
            "on-merge",
            "--release",
            "none",
            "--project",
            "cli",
            "--platform",
            hostCIPlatform(),
            "--channel",
            "stable",
            "--version",
            "v1.1.0",
            "--output-dir",
            stagedDir,
          ]);
          const stagedText = await readFile(join(stagedDir, "cli.txt"), "utf8");
          expect(stagedText).toContain("stable:1.1.0");
        },
      );
    });
  }
});
