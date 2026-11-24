/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag on-commit
 */
import { mkdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { beforeAll, beforeEach, describe, expect, it } from "vitest";
import {
  commitChange,
  configureLiveInfraWorkspace,
  copyScaffold,
  setupGenericInfraWorkspace,
  usePremiseHarness,
  useSuiteHarness,
  type LiveInfraContext,
} from "../../libs/testutils/fixtures.js";
import { isLiveOrRecordMode } from "../../libs/testutils/mode.js";
import { REPO_ROOT, TESTDATA_ROOT } from "../../libs/testutils/premise-harness.js";
import { hostCIPlatform, pathExists } from "../../libs/tsutils/fs.js";
import { assertSavedPulumiState, readGcsObject } from "../../libs/tsutils/gcsx.js";

// Fixtures -------------------------------------------------------------------

const ECOSYSTEM_FIXTURES = [
  { label: "bun", fixture: "workspaces/bun", tag: "bun" },
  { label: "rust", fixture: "workspaces/cargo", tag: "rust" },
  { label: "go", fixture: "workspaces/go", tag: "go" },
] as const;

// Tests ----------------------------------------------------------------------

describe("premise on-commit", { tags: ["e2e", "cli", "ci", "on-commit"] }, () => {
  describe("Given a project with infra and generic apps and libs", () => {
    describe(
      "When committing on a feature branch with [publish-rc]",
      { tags: ["smoke", "replay"] },
      () => {
        const suite = useSuiteHarness("on_commit_generic_infra_rc", import.meta.filename, {
          env: { PM_RELEASE: "A" },
        });
        let artifactsDir = "";
        let eventsFile = "";
        let planOutput = "";

        beforeAll(async () => {
          const h = suite();
          await setupGenericInfraWorkspace(h);
          await h.git(["switch", "-c", "feature/example"]);
          await commitChange(h, "feat: add static page [publish-rc]");
          const version = (await h.pm(["version", "rc", "--identifier", "1"])).stdout.trim();
          artifactsDir = join(h.outputDir, "artifacts-rc");
          eventsFile = join(h.outputDir, "events.log");
          await mkdir(artifactsDir, { recursive: true });

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
          planOutput = (await h.pm(["ci", "plan", "--ci-output"])).stdout;
          await h.writeManifest();
        });

        it("It should run build, test, format:check, lint, and release:build in order", async () => {
          expect(await readFile(eventsFile, "utf8")).toBe(
            "build::A\ntest::A\nformat:check::A\nlint::A\nrelease:build::A\n",
          );
        });

        it("It should stage RC artifacts locally", async () => {
          expect(await readFile(join(artifactsDir, "index.html"), "utf8")).toContain("Release A");
          expect(await readFile(join(artifactsDir, "version.txt"), "utf8")).toBe("rc:1.1.0-rc.1\n");
        });

        it("It should schedule infrastructure and migrations in CI plan", () => {
          expect(planOutput).toContain("has_infra=true\n");
          expect(planOutput).toContain("has_migrations=true\n");
          expect(planOutput).toContain("migration_matrix=");
        });
      },
    );

    describe("When a check or release:build fails", { tags: ["replay"] }, () => {
      const harness = usePremiseHarness({ env: { PM_RELEASE: "A" } });

      beforeEach(async () => {
        await setupGenericInfraWorkspace(harness());
      });

      it("It should halt before release:build on test failure and create no tags on release:build failure", async () => {
        // Arrange
        const h = harness();
        await h.git(["switch", "-c", "feature/failing-gates"]);
        await commitChange(h, "feat: broken commit [publish-rc]");
        const version = (await h.pm(["version", "rc", "--identifier", "1"])).stdout.trim();
        const failedCheckDir = join(h.outputDir, "artifacts-failed-check");
        const failedStageDir = join(h.outputDir, "artifacts-failed-stage");
        await mkdir(failedCheckDir, { recursive: true });
        await mkdir(failedStageDir, { recursive: true });

        // Act
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
            env: {
              PREMISE_TEST_FAIL_TASK: "release:build",
              PREMISE_ARTIFACT_DIR: failedStageDir,
            },
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
      });
    });

    describe(
      "When deploying a feature preview",
      { tags: ["gcp", "pulumi", "fnox", "live"] },
      () => {
        const suite = useSuiteHarness("on_commit_live_preview", import.meta.filename, {
          env: { PM_RELEASE: "A" },
          enabled: isLiveOrRecordMode(),
        });
        let ctx: LiveInfraContext;
        let bucket = "";
        let envLocal = "";
        let failBCombined = "";
        let failBCode: number | null = 0;

        beforeAll(async () => {
          if (!isLiveOrRecordMode()) return;
          const h = suite();
          ctx = await configureLiveInfraWorkspace(h, { copyFixture: true, initGit: true });
          ctx.touched.add("preview");
          await h.git(["switch", "-c", ctx.branch]);

          await h.pm(
            ["ci", "flow", "on-commit", "--environment", "preview", "--branch", ctx.branch],
            {
              env: { PM_RELEASE: "A" },
            },
          );
          bucket = (
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
          ctx.buckets.set("preview", bucket);

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
          envLocal = await h.readFile("apps/web/.env.local");

          const failB = await h.pmFail(
            ["ci", "flow", "on-deploy", "--environment", "preview", "--branch", ctx.branch],
            { env: { PM_RELEASE: "B", PREMISE_TEST_FAIL_HEALTH: "preview" } },
          );
          failBCode = failB.code;
          failBCombined = failB.combined;
          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        });

        it.skipIf(!isLiveOrRecordMode())(
          "It should save preview state on the configured alternate backend",
          async () => {
            await assertSavedPulumiState({
              backendUrl: ctx.alternateBackend,
              env: "preview",
              branch: ctx.branch,
              cwd: suite().workspace,
            });
          },
        );

        it.skipIf(!isLiveOrRecordMode())(
          "It should combine fnox secrets with queried preview outputs",
          () => {
            expect(envLocal).toContain('APP_MESSAGE="preview-example"');
            expect(envLocal).toContain(`BUCKET_NAME="${bucket}"`);
          },
        );

        it.skipIf(!isLiveOrRecordMode())(
          "It should roll back to Release A on health failure while keeping Migration B",
          async () => {
            const h = suite();
            expect(failBCode).not.toBe(0);
            expect(failBCombined).toContain("health");
            expect(await readGcsObject(bucket, "index.html", h.workspace)).toContain(
              "<h1>Release A</h1>",
            );
            expect((await readGcsObject(bucket, "schema-version.txt", h.workspace)).trim()).toBe(
              "preview:B",
            );
          },
        );
      },
    );
  });

  describe("Given a project without infra and generic apps and libs", { tags: ["replay"] }, () => {
    const suite = useSuiteHarness("on_commit_generic_no_infra", import.meta.filename, {
      env: { PM_RELEASE: "A" },
    });
    let planOutput = "";
    let events = "";

    beforeAll(async () => {
      const h = suite();
      await copyScaffold(join(TESTDATA_ROOT, "workspaces/generic-no-infra"), h.workspace);
      await h.initGit("chore: initial generic project", "v1.0.0");
      const eventsFile = join(h.outputDir, "events.log");
      planOutput = (await h.pm(["ci", "plan", "--ci-output"])).stdout;
      await h.pm(["ci", "flow", "on-commit", "--release", "none"], {
        env: { PREMISE_TEST_EVENTS: eventsFile },
      });
      events = await readFile(eventsFile, "utf8");
      await h.writeManifest();
    });

    it("It should plan has_infra=false", () => {
      expect(planOutput).toContain("has_infra=false\n");
    });

    it("It should run build, test, format:check, and lint across generic libs and apps", () => {
      expect(events).toContain("shared:build::A");
      expect(events).toContain("shared:test::A");
      expect(events).toContain("web:build::A");
      expect(events).toContain("web:test::A");
      expect(events).toContain("web:lint::A");
    });
  });

  for (const eco of ECOSYSTEM_FIXTURES) {
    describe(`Given a project with ${eco.label} libs`, { tags: [eco.tag, "replay"] }, () => {
      const suite = useSuiteHarness(`on_commit_${eco.label}`, import.meta.filename, {
        env: { PM_RELEASE: "A" },
      });
      let planOutput = "";
      let flowOutput = "";

      beforeAll(async () => {
        const h = suite();
        await copyScaffold(join(TESTDATA_ROOT, eco.fixture), h.workspace);
        await h.initGit(`chore: initial ${eco.label} project`, "v1.0.0");
        planOutput = (await h.pm(["ci", "plan", "--ci-output"])).stdout;
        flowOutput = (await h.pm(["ci", "flow", "on-commit", "--release", "none"])).combined;
        await h.writeManifest();
      });

      it("It should handle multiple platforms in CI planning (linux, macos, windows)", () => {
        expect(planOutput).toContain("has_infra=false\n");
        expect(planOutput).toContain("mode=matrix\n");
        expect(planOutput).toContain("native=true\n");
        expect(planOutput).toContain('"platform":"linux"');
        expect(planOutput).toContain('"platform":"macos"');
        expect(planOutput).toContain('"platform":"windows"');
      });

      it(`It should handle nobuild and local-only ${eco.label} projects during on-commit checks`, () => {
        expect(flowOutput).toContain("nobuild-lib:skip-build");
        expect(flowOutput).toContain("local-lib:test");
        expect(flowOutput).toContain("public-lib:test");
        expect(flowOutput).toContain("private-lib:test");
      });

      it(`It should handle public packages for the ${eco.label} registry [CLO-228..CLO-234]`, () => {
        expect(planOutput).toContain("has_registry_packages=true\n");
        expect(planOutput).toContain("has_github_release=true\n");
      });

      it(`It should handle private packages for Artifact Registry / alternate ${eco.label} registries [CLO-229, CLO-232..CLO-234]`, async () => {
        const workflow = await readFile(
          join(REPO_ROOT, "apps/premise/assets/github-workflows/on-commit.yml"),
          "utf8",
        );
        expect(workflow).toContain("NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}");
        expect(workflow).toContain("has_registry_packages");
        expect(workflow).not.toContain("install-premise: build");
      });

      it(`It should handle RC binary release staging and workflow parity for ${eco.label} [CLO-230, CLO-235, CLO-236]`, async () => {
        const h = suite();
        const stagedDir = join(h.outputDir, `staged-rc-${eco.label}`);
        await mkdir(stagedDir, { recursive: true });
        await h.pm([
          "ci",
          "flow",
          "on-commit",
          "--release",
          "none",
          "--project",
          "cli",
          "--platform",
          hostCIPlatform(),
          "--channel",
          "rc",
          "--version",
          "v1.1.0-rc.1",
          "--output-dir",
          stagedDir,
        ]);
        const stagedText = await readFile(join(stagedDir, "cli.txt"), "utf8");
        expect(stagedText).toContain("rc:1.1.0-rc.1");
      });
    });
  }
});
