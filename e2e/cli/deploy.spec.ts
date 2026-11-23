import { chmod, mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { usePremiseHarness } from "../common/fixtures.js";
import { TESTDATA_ROOT, type PremiseHarness } from "../common/premise-harness.js";
import { copyScaffold, isLiveOrRecordMode, isReplayMode } from "../common/test-utils.js";
import {
  assertGcsBucketDeleted,
  destroyPulumiStack,
  resolveLiveGcpConfig,
} from "../common/gcp-driver.js";

interface LiveInfraContext {
  branch: string;
  primaryBackend: string;
  alternateBackend: string;
  gcpProject: string;
  touched: Set<string>;
  buckets: Map<string, string>;
}

async function setupLiveInfraClient(h: PremiseHarness): Promise<LiveInfraContext> {
  const { project: gcpProject, backendUrl: baseBackend } = resolveLiveGcpConfig();
  const runBackend = `${baseBackend.replace(/\/+$/, "")}/run-${h.runId}`;
  const primaryBackend = runBackend;
  const alternateBackend = `${runBackend}/alternate`;
  const branch = `feature/static-${h.runId}`;

  await copyScaffold(join(TESTDATA_ROOT, "pulumi/client"), h.workspace);

  // Expose pm on PATH for client mise tasks that invoke pm
  const shimBin = join(h.outputDir, "pm-bin");
  await mkdir(shimBin, { recursive: true });
  await writeFile(join(shimBin, "pm"), `#!/bin/sh\nexec "${h.binary}" "$@"\n`, { mode: 0o755 });
  await chmod(join(shimBin, "pm"), 0o755);
  h.env.PATH = `${shimBin}:${h.env.PATH ?? process.env.PATH ?? ""}`;
  h.env.PULUMI_CONFIG_PASSPHRASE = "";
  h.env.PM_RELEASE = "A";
  h.env.PM_BRANCH = "";
  h.env.PULUMI_BACKEND_URL = `${runBackend}/unselected-fallback`;

  // Configure named primary and alternate backends in premise.yaml
  const manifestPath = join(h.workspace, "premise.yaml");
  const original = await readFile(manifestPath, "utf8");
  const infraBlock = `  infra:
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

  const updated = original.includes("  infra:")
    ? original.replace(/  infra:[\s\S]*?(?=\n  [a-z_]+:|\n[a-z_]+:|$)/, infraBlock)
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

describe("deploy and infrastructure lifecycle", () => {
  const harness = usePremiseHarness();

  describe("given provider authentication configurations", () => {
    it(
      "when authenticating GCP via WIF OIDC, then credential paths are written to env-file without raw tokens",
      { tags: ["e2e", "cli", "smoke", "deploy", "gcp", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await h.pm(["init"]);
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

        // In replay mode, shim gcloud so CI runners without gcloud still verify the exact WIF CLI contract
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
        const credPath = match![1].trim();
        const credInfo = await stat(credPath);
        expect(credInfo.mode & 0o777).toBe(0o600);
        await h.writeManifest();
      },
    );

    it(
      "when authenticating Azure or an unsupported provider, then native auth runs first and root ci:auth runs afterward",
      { tags: ["e2e", "cli", "deploy", "gcp", "azure", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await h.pm(["init"]);
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
          env: {
            GOOGLE_APPLICATION_CREDENTIALS: credFile,
          },
        });

        // Assert native GCP auth exported credentials before root ci:auth ran
        const order = await readFile(authOrderLog, "utf8");
        expect(order.trim()).toBe(`azure-ci-auth:${credFile}`);
        const envContent = await readFile(envFile, "utf8");
        expect(envContent).toContain(`GOOGLE_APPLICATION_CREDENTIALS=${credFile}`);
        await h.writeManifest();
      },
    );
  });

  describe("given a Pulumi workspace with --shared and primary/alternate GCS backends", () => {
    it(
      "when scaffolding infra with TypeScript runtime, then infra/Pulumi.yaml configures nodejs runtime",
      { tags: ["e2e", "cli", "deploy", "pulumi", "replay"] },
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
      "when running setup and plan, then tools install and plan previews without provisioning",
      { tags: ["e2e", "cli", "deploy", "gcp", "pulumi", "live"] },
      async () => {
        // Arrange
        const h = harness();
        const ctx = await setupLiveInfraClient(h);
        ctx.touched.add("dev");

        // Act
        await h.pm(["infra", "setup", "--env", "dev"]);
        const existsBeforePlan = (await h.pm(["infra", "exists", "--env", "dev"])).stdout.trim();
        const plan = (await h.pm(["infra", "plan", "--env", "dev"])).stdout;
        const existsAfterPlan = (await h.pm(["infra", "exists", "--env", "dev"])).stdout.trim();

        // Assert
        expect(existsBeforePlan).toBe("false");
        expect(plan).toContain("gcp:storage:Bucket");
        expect(existsAfterPlan).toBe("false");
        h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
        await h.writeManifest();
      },
    );

    it.skipIf(!isLiveOrRecordMode())(
      "when applying dev (rev a -> b), failing an apply, and querying from a fresh checkout, then updates mutate in-place and state survives failure",
      { tags: ["e2e", "cli", "deploy", "gcp", "pulumi", "live"] },
      async () => {
        // Arrange
        const h = harness();
        const ctx = await setupLiveInfraClient(h);
        ctx.touched.add("dev");
        await h.pm(["infra", "setup", "--env", "dev"]);

        // Act: apply rev a, then rev b
        await h.pm(["infra", "apply", "--env", "dev", "--yes"], {
          env: { PREMISE_TEST_REVISION: "a" },
        });
        const bucket = await queryBucket(h, ctx, "dev");
        await h.pm(["infra", "apply", "--env", "dev", "--yes"], {
          env: { PREMISE_TEST_REVISION: "b" },
        });

        // Assert in-place update
        expect(bucket.startsWith("storage-dev-")).toBe(true);
        expect(await queryBucket(h, ctx, "dev")).toBe(bucket);
        const revLabel = (
          await h.run(
            "gcloud",
            ["storage", "buckets", "describe", `gs://${bucket}`, "--format=value(labels.revision)"],
            { expectSuccess: true },
          )
        ).stdout.trim();
        expect(revLabel).toBe("b");

        // Act: failing apply preserves bucket
        const failed = await h.pmFail(["infra", "apply", "--env", "dev", "--yes"], {
          env: { PREMISE_TEST_FAIL_INFRA: "1" },
        });
        expect(failed.code).not.toBe(0);
        expect(failed.combined).toContain("requested infrastructure failure");
        expect(await queryBucket(h, ctx, "dev")).toBe(bucket);

        // Act: query from a fresh checkout
        const fresh = join(h.outputDir, "fresh-checkout");
        await copyScaffold(h.workspace, fresh);
        const freshOut = (
          await h.pm(["infra", "query", "--env", "dev", "--output", "bucketName"], { cwd: fresh })
        ).stdout.trim();
        expect(freshOut).toBe(bucket);

        h.updateRemoteManifest({
          gcpProject: ctx.gcpProject,
          pulumiStacks: [{ env: "dev", stack: "dev", backendUrl: ctx.primaryBackend, bucket }],
        });
        await h.writeManifest();
      },
    );
  });

  describe("given a live feature-branch preview environment", () => {
    it.skipIf(!isLiveOrRecordMode())(
      "when deploying release A, failing release B health check, and merging to main, then preview rolls back via GCS history and destroys idempotently",
      { tags: ["e2e", "cli", "deploy", "gcp", "pulumi", "fnox", "live"] },
      async () => {
        // Arrange
        const h = harness();
        const ctx = await setupLiveInfraClient(h);
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

        // Assert release A + migration A deployed
        const pageA = (
          await h.run("gcloud", ["storage", "cat", `gs://${bucket}/index.html`], {
            expectSuccess: true,
          })
        ).stdout;
        expect(pageA).toContain("<h1>Release A</h1>");

        // Act: deploy release B with failing health check
        const failB = await h.pmFail(
          ["ci", "flow", "on-deploy", "--environment", "preview", "--branch", ctx.branch],
          { env: { PM_RELEASE: "B", PREMISE_TEST_FAIL_HEALTH: "preview" } },
        );

        // Assert rollback restored Release A while keeping Migration B
        expect(failB.code).not.toBe(0);
        expect(failB.combined).toContain("health");
        const pageAfterRollback = (
          await h.run("gcloud", ["storage", "cat", `gs://${bucket}/index.html`], {
            expectSuccess: true,
          })
        ).stdout;
        expect(pageAfterRollback).toContain("<h1>Release A</h1>");
        const schemaAfterRollback = (
          await h.run("gcloud", ["storage", "cat", `gs://${bucket}/schema-version.txt`], {
            expectSuccess: true,
          })
        ).stdout.trim();
        expect(schemaAfterRollback).toBe("preview:B");

        // Act: merge preview cleanup runs twice idempotently
        await h.pm(["ci", "flow", "on-merge", "--environment", "preview", "--branch", ctx.branch]);
        await assertGcsBucketDeleted(bucket, h.workspace);
        await h.pm(["ci", "flow", "on-merge", "--environment", "preview", "--branch", ctx.branch]);
        ctx.touched.delete("preview");
        ctx.buckets.delete("preview");

        h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
        await h.writeManifest();
      },
    );
  });

  describe("given one immutable release B staged on main", () => {
    it.skipIf(!isLiveOrRecordMode())(
      "when promoting via on-deploy to dev, stage, and prod, then exact bytes promote in-place with migrate -> deploy -> health ordering",
      { tags: ["e2e", "cli", "deploy", "gcp", "pulumi", "live"] },
      async () => {
        // Arrange: stage release B once, then promote the exact bytes across dev, stage, and prod
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

          // Assert exact bytes, in-place bucket, and migrate -> deploy -> health order
          const deployedHtml = (
            await h.run("gcloud", ["storage", "cat", `gs://${bucket}/index.html`], {
              expectSuccess: true,
            })
          ).stdout;
          expect(deployedHtml).toBe(expectedHtml);
          expect(await queryBucket(h, ctx, env)).toBe(bucket);
          expect(await readFile(eventsFile, "utf8")).toBe(
            `migrate:${env}:B\ndeploy:${env}:B\nhealth:${env}:B\n`,
          );
        }

        h.updateRemoteManifest({ gcpProject: ctx.gcpProject });
        await h.writeManifest();
      },
    );
  });
});
