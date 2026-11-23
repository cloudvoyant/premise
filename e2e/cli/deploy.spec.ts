/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag deploy
 */
import { createHash } from "node:crypto";
import { chmod, mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { usePremiseHarness } from "../common/fixtures.js";
import {
  assertGcsBucketDeleted,
  destroyPulumiStack,
  resolveLiveGcpConfig,
} from "../common/gcp-driver.js";
import { TESTDATA_ROOT, type PremiseHarness } from "../common/premise-harness.js";
import { copyScaffold, isLiveOrRecordMode, isReplayMode } from "../common/test-utils.js";

// Fixtures -------------------------------------------------------------------

interface LiveInfraContext {
  branch: string;
  primaryBackend: string;
  alternateBackend: string;
  gcpProject: string;
  touched: Set<string>;
  buckets: Map<string, string>;
}

// Tests ----------------------------------------------------------------------

describe("deploy and infrastructure lifecycle", { tags: ["e2e", "cli", "deploy"] }, () => {
  const harness = usePremiseHarness();

  describe("given provider authentication configurations", { tags: ["gcp", "replay"] }, () => {
    beforeEach(async () => {
      await harness().pm(["init"]);
    });

    it(
      "when authenticating GCP via WIF OIDC, then credential paths are written to env-file without raw tokens",
      { tags: ["smoke"] },
      async () => {
        // Arrange
        const h = harness();
        const manifest = await h.readFile("premise.yaml");
        await h.writeFile(
          "premise.yaml",
          `${manifest.trimEnd()}\n    infra:\n        providers:\n            - gcp\n`,
        );

        const tokenFile = join(h.outputDir, "oidc-token");
        const envFile = join(h.outputDir, "ci.env");
        const rawOidcToken = "eyJhbGciOiJSUzI1NiJ9.super-secret-oidc-payload.sig";
        await writeFile(tokenFile, rawOidcToken, { mode: 0o600 });
        await writeFile(envFile, "", "utf8");

        const env: Record<string, string | undefined> = {
          GOOGLE_APPLICATION_CREDENTIALS: "",
          CI_OIDC_TOKEN_FILE: tokenFile,
          GCP_WORKLOAD_IDENTITY_PROVIDER:
            "projects/547304404433/locations/global/workloadIdentityPools/github/providers/premise",
          GCP_SERVICE_ACCOUNT: "premise-ci@cloudvoyant-premise.iam.gserviceaccount.com",
        };

        if (isReplayMode()) {
          const shimBin = join(h.outputDir, "gcloud-shim");
          await mkdir(shimBin, { recursive: true });
          await writeFile(
            join(shimBin, "gcloud"),
            `#!/bin/sh
set -eu
out=""
for arg in "$@"; do
  case "$arg" in
    --output-file=*) out="\${arg#--output-file=}" ;;
  esac
done
[ -n "$out" ]
printf '{"type":"external_account","credential_source":{"file":"%s"}}\n' "$CI_OIDC_TOKEN_FILE" > "$out"
`,
            { mode: 0o755 },
          );
          await chmod(join(shimBin, "gcloud"), 0o755);
          env.PATH = `${shimBin}:${process.env.PATH ?? ""}`;
        }

        // Act
        const result = await h.pm(["infra", "auth", "--ci", "--env-file", envFile], { env });

        // Assert
        const envContent = await readFile(envFile, "utf8");
        expect(envContent).toContain("GOOGLE_APPLICATION_CREDENTIALS=");
        expect(envContent).toContain("CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=");
        expect(envContent).not.toContain(rawOidcToken);
        expect(result.combined).not.toContain(rawOidcToken);

        const match = envContent.match(/^GOOGLE_APPLICATION_CREDENTIALS=(.+)$/m);
        expect(match).toBeTruthy();
        expect((await stat(match![1].trim())).mode & 0o777).toBe(0o600);
        await h.writeManifest();
      },
    );

    it(
      "when authenticating Azure or an unsupported provider, then native auth runs first and root ci:auth runs afterward",
      { tags: ["azure"] },
      async () => {
        // Arrange
        const h = harness();
        const manifest = await h.readFile("premise.yaml");
        await h.writeFile(
          "premise.yaml",
          `${manifest.trimEnd()}\n    infra:\n        providers:\n            - gcp\n            - azure\n`,
        );

        const credFile = join(h.outputDir, "gcp-adc.json");
        await writeFile(credFile, '{"type":"authorized_user"}\n', { mode: 0o600 });
        const authOrderLog = join(h.outputDir, "auth-order.log");
        const miseContent = await h.readFile("mise.toml");
        await h.writeFile(
          "mise.toml",
          `${miseContent}\n[tasks."ci:auth"]\nrun = 'printf "azure-ci-auth:%s\\n" "$GOOGLE_APPLICATION_CREDENTIALS" >> "${authOrderLog}"'\n`,
        );
        const envFile = join(h.outputDir, "azure-ci.env");
        await writeFile(envFile, "", "utf8");

        // Act
        await h.pm(["infra", "auth", "--ci", "--env-file", envFile], {
          env: { GOOGLE_APPLICATION_CREDENTIALS: credFile },
        });

        // Assert
        expect((await readFile(authOrderLog, "utf8")).trim()).toBe(`azure-ci-auth:${credFile}`);
        expect(await readFile(envFile, "utf8")).toContain(
          `GOOGLE_APPLICATION_CREDENTIALS=${credFile}`,
        );
        await h.writeManifest();
      },
    );
  });

  describe("given a fresh workspace scaffolding Pulumi infra", { tags: ["pulumi"] }, () => {
    it(
      "when scaffolding infra with TypeScript runtime, then infra/Pulumi.yaml configures nodejs runtime",
      { tags: ["replay"] },
      async () => {
        // Arrange
        const h = harness();
        await h.pm(["init"]);

        // Act
        await h.pm(["infra", "setup", "--pulumi-lang", "typescript"]);

        // Assert
        const pulumiYaml = await h.readFile("infra/Pulumi.yaml");
        expect(pulumiYaml).toContain("runtime:");
        expect(pulumiYaml).toContain("nodejs");
        await h.writeManifest();
      },
    );

    it.skipIf(!isLiveOrRecordMode())(
      "when scaffolding an app with pm init --infra --pulumi-lang go, then empty starter setup, plan, apply, and output succeed",
      { tags: ["gcp", "live"] },
      async () => {
        // Arrange
        const h = harness();
        const registry = join(h.outputDir, "registry");
        await mkdir(registry, { recursive: true });
        await h.pm(["init", "--kind", "template-registry"], { cwd: registry });
        await h.pm(["template", "init", "app"], { cwd: registry });
        await copyScaffold(join(TESTDATA_ROOT, "scaffold/app"), join(registry, "templates/app"));
        await h.pm(["init", "--infra", "--pulumi-lang", "go"]);
        await h.pm(["generate", `${registry}:app`, "--answer", "name=example"]);
        const ctx = await configureLiveInfraManifest(h);
        ctx.touched.add("dev");

        // Act
        await h.pm(["infra", "setup", "--env", "dev"]);
        await h.pm(["infra", "plan", "--env", "dev"]);
        await h.pm(["infra", "apply", "--env", "dev", "--yes"]);
        const output = (await h.pm(["infra", "output", "--env", "dev"])).stdout.trim();

        // Assert
        expect(await h.readFile("infra/Pulumi.yaml")).toContain("runtime: go");
        expect(await h.readFile("mise.toml")).toContain("pulumi");
        expect(output).toBe("{}");
        h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
        await h.writeManifest();
      },
    );
  });

  describe(
    "given a Pulumi workspace with primary and alternate GCS backends",
    { tags: ["gcp", "pulumi", "live"] },
    () => {
      let ctx: LiveInfraContext;

      beforeEach(async () => {
        if (!isLiveOrRecordMode()) return;
        ctx = await setupLiveInfraClient(harness());
      });

      it.skipIf(!isLiveOrRecordMode())(
        "when running setup and plan, then tools install and plan previews without provisioning",
        async () => {
          // Arrange
          const h = harness();
          ctx.touched.add("dev");

          // Act
          await h.pm(["infra", "setup", "--env", "dev"]);
          const existsBefore = (await h.pm(["infra", "exists", "--env", "dev"])).stdout.trim();
          const plan = (await h.pm(["infra", "plan", "--env", "dev"])).stdout;
          const existsAfter = (await h.pm(["infra", "exists", "--env", "dev"])).stdout.trim();

          // Assert
          expect(existsBefore).toBe("false");
          expect(plan).toContain("gcp:storage:Bucket");
          expect(existsAfter).toBe("false");
          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        },
      );

      it.skipIf(!isLiveOrRecordMode())(
        "when applying across dev, stage, prod, and preview (rev a -> b), then updates mutate in-place on configured backends",
        async () => {
          // Arrange & Act & Assert across all 4 environments
          const h = harness();
          for (const env of ["dev", "stage", "prod", "preview"] as const) {
            ctx.touched.add(env);
            await h.pm(["infra", "setup", ...envScopeArgs(ctx, env)]);
            await h.pm(["infra", "apply", ...envScopeArgs(ctx, env), "--yes"], {
              env: { PREMISE_TEST_REVISION: "a" },
            });
            const bucket = await queryBucket(h, ctx, env);

            await h.pm(["infra", "apply", ...envScopeArgs(ctx, env), "--yes"], {
              env: { PREMISE_TEST_REVISION: "b" },
            });

            const expectedPrefix =
              env === "preview" ? `storage-${ctx.branch.split("/").at(1)}-` : `storage-${env}-`;
            expect(bucket.startsWith(expectedPrefix)).toBe(true);
            expect(await queryBucket(h, ctx, env)).toBe(bucket);
            const revLabel = (
              await h.run(
                "gcloud",
                [
                  "storage",
                  "buckets",
                  "describe",
                  `gs://${bucket}`,
                  "--format=value(labels.revision)",
                ],
                { expectSuccess: true },
              )
            ).stdout.trim();
            expect(revLabel).toBe("b");
            await assertSavedBackend(h, ctx, env);
          }

          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        },
      );

      it.skipIf(!isLiveOrRecordMode())(
        "when an apply fails, then previously provisioned resources remain intact",
        async () => {
          // Arrange
          const h = harness();
          ctx.touched.add("dev");
          await h.pm(["infra", "setup", "--env", "dev"]);
          await h.pm(["infra", "apply", "--env", "dev", "--yes"]);
          const bucket = await queryBucket(h, ctx, "dev");

          // Act
          const failed = await h.pmFail(["infra", "apply", "--env", "dev", "--yes"], {
            env: { PREMISE_TEST_FAIL_INFRA: "1" },
          });

          // Assert
          expect(failed.code).not.toBe(0);
          expect(failed.combined).toContain("requested infrastructure failure");
          expect(await queryBucket(h, ctx, "dev")).toBe(bucket);
          await h.run("gcloud", ["storage", "buckets", "describe", `gs://${bucket}`], {
            expectSuccess: true,
          });
          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        },
      );

      it.skipIf(!isLiveOrRecordMode())(
        "when querying from local and fresh checkouts, then exported outputs and resource properties match remote state",
        async () => {
          // Arrange
          const h = harness();
          ctx.touched.add("stage");
          await h.pm(["infra", "setup", "--env", "stage"]);
          await h.pm(["infra", "apply", "--env", "stage", "--yes"]);
          const bucket = await queryBucket(h, ctx, "stage");

          // Act
          const byOutput = (
            await h.pm(["infra", "query", "--env", "stage", "--output", "bucketName"])
          ).stdout.trim();
          const byResource = (
            await h.pm([
              "infra",
              "query",
              "--env",
              "stage",
              "--resource",
              "storage-stage",
              "--property",
              "name",
            ])
          ).stdout.trim();
          const fresh = join(h.outputDir, "fresh-checkout");
          await copyScaffold(h.workspace, fresh);
          const fromFresh = (
            await h.pm(["infra", "query", "--env", "stage", "--output", "bucketName"], {
              cwd: fresh,
            })
          ).stdout.trim();

          // Assert
          expect(byOutput).toBe(bucket);
          expect(byResource).toBe(bucket);
          expect(fromFresh).toBe(bucket);
          h.updateRemoteManifest({
            gcpProject: ctx.gcpProject,
            pulumiStacks: [
              { env: "stage", stack: "stage", backendUrl: ctx.primaryBackend, bucket },
            ],
          });
          await h.writeManifest();
        },
      );

      it.skipIf(!isLiveOrRecordMode())(
        "when destroying preview infra twice, then the bucket is deleted and repeated destroy succeeds idempotently",
        async () => {
          // Arrange
          const h = harness();
          ctx.touched.add("preview");
          await h.pm(["infra", "setup", ...envScopeArgs(ctx, "preview")]);
          await h.pm(["infra", "apply", ...envScopeArgs(ctx, "preview"), "--yes"]);
          const bucket = await queryBucket(h, ctx, "preview");

          // Act
          await h.pm(["infra", "destroy", ...envScopeArgs(ctx, "preview"), "--yes"]);
          await assertGcsBucketDeleted(bucket, h.workspace);
          await h.pm(["infra", "destroy", ...envScopeArgs(ctx, "preview"), "--yes"]);

          // Assert
          ctx.touched.delete("preview");
          ctx.buckets.delete("preview");
          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        },
      );
    },
  );

  describe(
    "given a live feature-branch preview environment",
    { tags: ["gcp", "pulumi", "fnox", "live"] },
    () => {
      let ctx: LiveInfraContext;

      beforeEach(async () => {
        if (!isLiveOrRecordMode()) return;
        ctx = await setupLiveInfraClient(harness());
      });

      it.skipIf(!isLiveOrRecordMode())(
        "when deploying release A, pulling preview secrets, failing release B health check, and merging to main, then preview rolls back via GCS history and destroys idempotently",
        async () => {
          // Arrange
          const h = harness();
          ctx.touched.add("preview");
          await h.git(["switch", "-c", ctx.branch]);

          // Act: deploy release A to preview
          await h.pm(
            ["ci", "flow", "on-commit", "--environment", "preview", "--branch", ctx.branch],
            {
              env: { PM_RELEASE: "A" },
            },
          );
          const bucket = await queryBucket(h, ctx, "preview");
          await assertSavedBackend(h, ctx, "preview");

          // Assert release A, migration A, and fnox + Pulumi output combination
          expect(await readBucketObject(h, bucket, "index.html")).toContain("<h1>Release A</h1>");
          expect((await readBucketObject(h, bucket, "schema-version.txt")).trim()).toBe(
            "preview:A",
          );
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

          // Act: deploy release B with failing health check
          const failB = await h.pmFail(
            ["ci", "flow", "on-deploy", "--environment", "preview", "--branch", ctx.branch],
            { env: { PM_RELEASE: "B", PREMISE_TEST_FAIL_HEALTH: "preview" } },
          );

          // Assert rollback restored Release A while keeping Migration B
          expect(failB.code).not.toBe(0);
          expect(failB.combined).toContain("health");
          expect(await readBucketObject(h, bucket, "index.html")).toContain("<h1>Release A</h1>");
          expect((await readBucketObject(h, bucket, "schema-version.txt")).trim()).toBe(
            "preview:B",
          );

          // Act: merge preview cleanup runs twice idempotently
          await h.pm([
            "ci",
            "flow",
            "on-merge",
            "--environment",
            "preview",
            "--branch",
            ctx.branch,
          ]);
          await assertGcsBucketDeleted(bucket, h.workspace);
          await h.pm([
            "ci",
            "flow",
            "on-merge",
            "--environment",
            "preview",
            "--branch",
            ctx.branch,
          ]);
          ctx.touched.delete("preview");
          ctx.buckets.delete("preview");

          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        },
      );

      it.skipIf(!isLiveOrRecordMode())(
        "when merging to main after preview and dev A deployments, then preview destroys idempotently and dev updates in-place to release B",
        async () => {
          // Arrange
          const h = harness();
          ctx.touched.add("preview");
          ctx.touched.add("dev");
          await h.pm(
            ["ci", "flow", "on-commit", "--environment", "preview", "--branch", ctx.branch],
            {
              env: { PM_RELEASE: "A" },
            },
          );
          const previewBucket = await queryBucket(h, ctx, "preview");
          await h.pm(["ci", "flow", "on-deploy", "--environment", "dev"], {
            env: { PM_RELEASE: "A" },
          });
          const devBucket = await queryBucket(h, ctx, "dev");

          // Act
          await h.pm(
            ["ci", "flow", "on-merge", "--environment", "preview", "--branch", ctx.branch],
            {
              env: { PM_RELEASE: "A" },
            },
          );
          await h.pm(["ci", "flow", "on-merge", "--environment", "dev"], {
            env: { PM_RELEASE: "B" },
          });

          // Assert preview destroyed (idempotent) and dev updated in-place to B
          await assertGcsBucketDeleted(previewBucket, h.workspace);
          await h.pm(
            ["ci", "flow", "on-merge", "--environment", "preview", "--branch", ctx.branch],
            {
              env: { PM_RELEASE: "A" },
            },
          );
          ctx.touched.delete("preview");
          ctx.buckets.delete("preview");

          expect(await queryBucket(h, ctx, "dev")).toBe(devBucket);
          expect((await readBucketObject(h, devBucket, "schema-version.txt")).trim()).toBe("dev:B");
          expect(await readBucketObject(h, devBucket, "index.html")).toContain(
            "<h1>Release B</h1>",
          );
          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        },
      );
    },
  );

  describe(
    "given one immutable release B staged on main",
    { tags: ["gcp", "pulumi", "live"] },
    () => {
      it.skipIf(!isLiveOrRecordMode())(
        "when promoting via on-deploy to dev, stage, and prod, then exact bytes promote in-place with migrate -> deploy -> health ordering",
        async () => {
          // Arrange
          const h = harness();
          const ctx = await setupLiveInfraClient(h);
          const stagedDir = join(h.outputDir, "staged-release-B");
          await mkdir(stagedDir, { recursive: true });
          const platform =
            process.platform === "darwin"
              ? "macos"
              : process.platform === "win32"
                ? "windows"
                : "linux";

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
            const bucket = await queryBucket(h, ctx, env);
            const eventsFile = join(h.outputDir, `events-${env}.log`);

            // Act
            await h.pm(["ci", "flow", "on-deploy", "--environment", env], {
              env: { PM_RELEASE: "B", PREMISE_TEST_EVENTS: eventsFile },
            });

            // Assert exact bytes, in-place bucket, migrate -> deploy -> health order, and backend state
            expect(await readBucketObject(h, bucket, "index.html")).toBe(expectedHtml);
            expect(await queryBucket(h, ctx, env)).toBe(bucket);
            expect(await readFile(eventsFile, "utf8")).toBe(
              `migrate:${env}:B\ndeploy:${env}:B\nhealth:${env}:B\n`,
            );
            await assertSavedBackend(h, ctx, env);
          }

          h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
          await h.writeManifest();
        },
      );
    },
  );
});

// Private helpers ------------------------------------------------------------

async function setupLiveInfraClient(h: PremiseHarness): Promise<LiveInfraContext> {
  await copyScaffold(join(TESTDATA_ROOT, "pulumi/client"), h.workspace);
  return configureLiveInfraManifest(h);
}

async function configureLiveInfraManifest(h: PremiseHarness): Promise<LiveInfraContext> {
  const { project: gcpProject, backendUrl: baseBackend } = resolveLiveGcpConfig();
  const runBackend = `${baseBackend.replace(/\/+$/, "")}/run-${h.runId}`;
  const primaryBackend = runBackend;
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
  h.env.PULUMI_BACKEND_URL = `${runBackend}/unselected-fallback`;

  const manifestPath = join(h.workspace, "premise.yaml");
  const original = await readFile(manifestPath, "utf8");
  const infraBlock = `    infra:
        backend: primary
        backends:
            primary:
                url: ${primaryBackend}
            alternate:
                url: ${alternateBackend}
        environments:
            dev:
                backend: primary
                config:
                    gcp:project: ${gcpProject}
            stage:
                backend: primary
                config:
                    gcp:project: ${gcpProject}
            prod:
                backend: alternate
                config:
                    gcp:project: ${gcpProject}
            preview:
                backend: alternate
                config:
                    gcp:project: ${gcpProject}`;

  const updated = original.includes("infra:")
    ? original.replace(/[ ]+infra:[\s\S]*?(?=\n[ ]{2,4}[a-z_]+:|\n[a-z_]+:|$)/, infraBlock)
    : `${original.trimEnd()}\n${infraBlock}\n`;
  await writeFile(manifestPath, updated, "utf8");
  await h.initGit();

  const ctx: LiveInfraContext = {
    branch,
    primaryBackend,
    alternateBackend,
    gcpProject,
    touched: new Set(),
    buckets: new Map(),
  };

  h.registerRemoteCleanup(async () => {
    for (const env of ctx.touched) {
      const backendUrl =
        env === "prod" || env === "preview" ? ctx.alternateBackend : ctx.primaryBackend;
      if (env === "preview") {
        await h.pmFail(["ci", "flow", "on-merge", "--environment", "preview", "--branch", branch]);
      } else {
        await destroyPulumiStack({
          infraDir: join(h.workspace, "infra"),
          stack: env,
          backendUrl,
          env: h.env,
        });
      }
      const bucket = ctx.buckets.get(env);
      if (bucket) {
        await assertGcsBucketDeleted(bucket, h.workspace);
      }
    }
  });

  return ctx;
}

function envScopeArgs(ctx: LiveInfraContext, env: string): string[] {
  return env === "preview" ? ["--env", env, "--branch", ctx.branch] : ["--env", env];
}

async function queryBucket(h: PremiseHarness, ctx: LiveInfraContext, env: string): Promise<string> {
  const res = await h.pm(["infra", "query", ...envScopeArgs(ctx, env), "--output", "bucketName"]);
  const bucket = res.stdout.trim();
  expect(bucket).toBeTruthy();
  ctx.buckets.set(env, bucket);
  return bucket;
}

async function readBucketObject(
  h: PremiseHarness,
  bucket: string,
  object: string,
): Promise<string> {
  return (
    await h.run("gcloud", ["storage", "cat", `gs://${bucket}/${object}`], { expectSuccess: true })
  ).stdout;
}

async function assertSavedBackend(
  h: PremiseHarness,
  ctx: LiveInfraContext,
  env: "dev" | "stage" | "prod" | "preview",
): Promise<void> {
  const backendUrl =
    env === "prod" || env === "preview" ? ctx.alternateBackend : ctx.primaryBackend;
  const stack =
    env === "preview"
      ? `preview-${ctx.branch.replace(/^feature\//, "")}-${createHash("sha256").update(ctx.branch).digest("hex").slice(0, 10)}`
      : env;
  const statePath = `${backendUrl}/.pulumi/stacks/premise-gcs-fixture/${stack}.json`;
  await h.run("gcloud", ["storage", "objects", "describe", statePath, "--format=value(name)"], {
    expectSuccess: true,
  });
}
