/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag on-deploy
 */
import { mkdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { beforeAll, describe, expect, it } from "vitest";
import {
  configureLiveInfraWorkspace,
  copyScaffold,
  useSuiteHarness,
  type LiveInfraContext,
} from "../../libs/testutils/fixtures.js";
import { isLiveOrRecordMode } from "../../libs/testutils/mode.js";
import { TESTDATA_ROOT } from "../../libs/testutils/premise-harness.js";
import { hostCIPlatform } from "../../libs/tsutils/fs.js";
import { assertSavedPulumiState, readGcsObject } from "../../libs/tsutils/gcsx.js";

// Tests ----------------------------------------------------------------------

describe("premise on-deploy", { tags: ["e2e", "cli", "ci", "on-deploy"] }, () => {
  describe("Given a project with infra and generic apps and libs", () => {
    describe(
      "When promoting immutable release B across dev, stage, and prod",
      { tags: ["gcp", "pulumi", "live"] },
      () => {
        const suite = useSuiteHarness("on_deploy_promote_live", import.meta.filename, {
          env: { PM_RELEASE: "A" },
          enabled: isLiveOrRecordMode(),
        });
        let ctx: LiveInfraContext;
        let expectedHtml = "";
        const bucketsBefore = new Map<string, string>();
        const eventsByEnv = new Map<string, string>();

        beforeAll(async () => {
          if (!isLiveOrRecordMode()) return;
          const h = suite();
          ctx = await configureLiveInfraWorkspace(h, { copyFixture: true, initGit: true });
          const stagedDir = join(h.outputDir, "staged-release-B");
          await mkdir(stagedDir, { recursive: true });
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
              "v1.1.0",
              "--output-dir",
              stagedDir,
            ],
            { env: { PM_RELEASE: "B" } },
          );
          await copyScaffold(stagedDir, join(h.workspace, "apps/web/releases/B"));
          expectedHtml = await readFile(join(stagedDir, "index.html"), "utf8");

          for (const env of ["dev", "stage", "prod"] as const) {
            ctx.touched.add(env);
            await h.pm(["ci", "flow", "on-deploy", "--environment", env], {
              env: { PM_RELEASE: "A" },
            });
            const bucket = (
              await h.pm(["infra", "query", "--env", env, "--output", "bucketName"])
            ).stdout.trim();
            ctx.buckets.set(env, bucket);
            bucketsBefore.set(env, bucket);

            const eventsFile = join(h.outputDir, `events-${env}.log`);
            await h.pm(["ci", "flow", "on-deploy", "--environment", env], {
              env: { PM_RELEASE: "B", PREMISE_TEST_EVENTS: eventsFile },
            });
            eventsByEnv.set(env, await readFile(eventsFile, "utf8"));
          }

          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        });

        it.skipIf(!isLiveOrRecordMode())(
          "It should promote exact staged bytes in-place to dev, stage, and prod",
          async () => {
            const h = suite();
            for (const env of ["dev", "stage", "prod"] as const) {
              const bucket = bucketsBefore.get(env)!;
              expect(await readGcsObject(bucket, "index.html", h.workspace)).toBe(expectedHtml);
              expect(
                (
                  await h.pm(["infra", "query", "--env", env, "--output", "bucketName"])
                ).stdout.trim(),
              ).toBe(bucket);
            }
          },
        );

        it.skipIf(!isLiveOrRecordMode())(
          "It should execute migrate -> deploy -> health in order for each environment",
          () => {
            for (const env of ["dev", "stage", "prod"] as const) {
              expect(eventsByEnv.get(env)).toBe(
                `migrate:${env}:B\ndeploy:${env}:B\nhealth:${env}:B\n`,
              );
            }
          },
        );

        it.skipIf(!isLiveOrRecordMode())(
          "It should save stack state in each environment's configured backend",
          async () => {
            const h = suite();
            for (const env of ["dev", "stage", "prod"] as const) {
              await assertSavedPulumiState({
                backendUrl: env === "prod" ? ctx.alternateBackend : ctx.primaryBackend,
                env,
                cwd: h.workspace,
              });
            }
          },
        );
      },
    );
  });

  describe("Given a project without infra and generic apps and libs", { tags: ["replay"] }, () => {
    describe("When running on-deploy for stage", () => {
      const suite = useSuiteHarness("on_deploy_generic_no_infra", import.meta.filename, {
        env: { PM_RELEASE: "A" },
      });
      let planOutput = "";
      let deployResult: { code: number | null; combined: string };

      beforeAll(async () => {
        const h = suite();
        await copyScaffold(join(TESTDATA_ROOT, "workspaces/generic-no-infra"), h.workspace);
        await h.initGit("chore: initial generic project", "v1.0.0");
        planOutput = (await h.pm(["ci", "plan", "--ci-output"])).stdout;
        deployResult = await h.pmFail(["ci", "flow", "on-deploy", "--environment", "stage"]);
        await h.writeManifest();
      });

      it("It should report has_infra=false in CI plan", () => {
        expect(planOutput).toContain("has_infra=false\n");
      });

      it("It should reject on-deploy because workspace.infra is not configured", () => {
        expect(deployResult.code).not.toBe(0);
        expect(deployResult.combined).toContain("environment CI flows require workspace.infra");
      });
    });
  });
});
