/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag on-deploy
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

// Tests ----------------------------------------------------------------------

describe("premise on-deploy", { tags: ["e2e", "cli", "ci", "on-deploy"] }, () => {
  const harness = usePremiseHarness({ env: { PM_RELEASE: "A" } });

  describe("Given a project with infra and generic apps and libs", () => {
    it.skipIf(!isLiveOrRecordMode())(
      "It promotes one immutable release B across dev, stage, and prod in-place with migrate -> deploy -> health ordering",
      { tags: ["gcp", "pulumi", "live"] },
      async () => {
        // Arrange: stage release B once, then promote the exact bytes across dev, stage, and prod
        const h = harness();
        const ctx = await setupLiveDeployClient(h);
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
        const expectedHtml = await readFile(join(stagedDir, "index.html"), "utf8");

        for (const env of ["dev", "stage", "prod"] as const) {
          ctx.touched.add(env);
          await h.pm(["ci", "flow", "on-deploy", "--environment", env], {
            env: { PM_RELEASE: "A" },
          });
          const bucket = (
            await h.pm(["infra", "query", "--env", env, "--output", "bucketName"])
          ).stdout.trim();
          ctx.buckets.set(env, bucket);
          const eventsFile = join(h.outputDir, `events-${env}.log`);

          // Act
          await h.pm(["ci", "flow", "on-deploy", "--environment", env], {
            env: { PM_RELEASE: "B", PREMISE_TEST_EVENTS: eventsFile },
          });

          // Assert exact bytes, in-place bucket, migrate -> deploy -> health order, and saved backend
          const deployedHtml = (
            await h.run("gcloud", ["storage", "cat", `gs://${bucket}/index.html`], {
              expectSuccess: true,
            })
          ).stdout;
          expect(deployedHtml).toBe(expectedHtml);
          expect(
            (await h.pm(["infra", "query", "--env", env, "--output", "bucketName"])).stdout.trim(),
          ).toBe(bucket);
          expect(await readFile(eventsFile, "utf8")).toBe(
            `migrate:${env}:B\ndeploy:${env}:B\nhealth:${env}:B\n`,
          );
          const backendUrl = env === "prod" ? ctx.alternateBackend : ctx.primaryBackend;
          await h.run(
            "gcloud",
            [
              "storage",
              "objects",
              "describe",
              `${backendUrl}/.pulumi/stacks/premise-gcs-fixture/${env}.json`,
              "--format=value(name)",
            ],
            { expectSuccess: true },
          );
        }

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

    it("It reports has_infra=false in CI plan so manual infra deployment is omitted", async () => {
      // Arrange
      const h = harness();

      // Act
      const plan = await h.pm(["ci", "plan", "--ci-output"]);

      // Assert
      expect(plan.stdout).toContain("has_infra=false\n");
      await h.writeManifest();
    });
  });
});

// Private helpers ------------------------------------------------------------

function hostCIPlatform(): string {
  if (process.platform === "darwin") return "macos";
  if (process.platform === "win32") return "windows";
  return "linux";
}

async function setupLiveDeployClient(h: PremiseHarness) {
  await copyScaffold(join(TESTDATA_ROOT, "pulumi/client"), h.workspace);
  const { project: gcpProject, backendUrl: baseBackend } = resolveLiveGcpConfig();
  const primaryBackend = `${baseBackend.replace(/\/+$/, "")}/run-${h.runId}`;
  const alternateBackend = `${primaryBackend}/alternate`;

  const shimBin = join(h.outputDir, "pm-bin");
  await mkdir(shimBin, { recursive: true });
  await writeFile(join(shimBin, "pm"), `#!/bin/sh\nexec "${h.binary}" "$@"\n`, { mode: 0o755 });
  await chmod(join(shimBin, "pm"), 0o755);
  h.env.PATH = `${shimBin}:${h.env.PATH ?? process.env.PATH ?? ""}`;
  h.env.PULUMI_CONFIG_PASSPHRASE = "";
  h.env.PM_RELEASE = "A";

  const manifestPath = join(h.workspace, "premise.yaml");
  const original = await readFile(manifestPath, "utf8");
  const updated = patchManifestInfra(original, {
    primaryBackend,
    alternateBackend,
    gcpProject,
  });
  await writeFile(manifestPath, updated, "utf8");
  await h.initGit("chore: initial project", "v1.0.0");

  const ctx = {
    gcpProject,
    primaryBackend,
    alternateBackend,
    touched: new Set<"dev" | "stage" | "prod">(),
    buckets: new Map<string, string>(),
  };

  h.registerRemoteCleanup(async () => {
    for (const env of ctx.touched) {
      const backendUrl = env === "prod" ? alternateBackend : primaryBackend;
      await destroyPulumiStack({
        infraDir: join(h.workspace, "infra"),
        stack: env,
        backendUrl,
        env: h.env,
      }).catch(() => {});
      const bucket = ctx.buckets.get(env);
      if (bucket) {
        await assertGcsBucketDeleted(bucket, h.workspace);
      }
    }
  });

  return ctx;
}
