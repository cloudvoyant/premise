/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag secrets
 */
import { chmod, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { usePremiseHarness } from "../../libs/testutils/fixtures.js";
import { isLiveOrRecordMode } from "../../libs/testutils/mode.js";
import { TESTDATA_ROOT, type PremiseHarness } from "../../libs/testutils/premise-harness.js";
import { copyScaffold } from "../../libs/tsutils/fs.js";
import { resolveLiveGcpConfig } from "../../libs/tsutils/gcs.js";

// Fixtures -------------------------------------------------------------------

const CLEAN_CI_ENV: Record<string, string | undefined> = {
  CI: "",
  GITHUB_ACTIONS: "",
  GITLAB_CI: "",
};

interface SecretProviderMatrixRow {
  name: "local-json" | "gcp-secret-manager";
  liveOnly: boolean;
  tags: string[];
  environments: readonly ("dev" | "stage" | "prod" | "preview")[];
}

const SECRET_PROVIDERS: readonly SecretProviderMatrixRow[] = [
  {
    name: "local-json",
    liveOnly: false,
    tags: ["replay"],
    environments: ["dev", "stage", "prod", "preview"],
  },
  {
    name: "gcp-secret-manager",
    liveOnly: true,
    tags: ["gcp", "live"],
    environments: ["dev"],
  },
];

// Tests ----------------------------------------------------------------------

describe("premise secrets", { tags: ["e2e", "cli", "secrets", "fnox"] }, () => {
  const harness = usePremiseHarness({ env: CLEAN_CI_ENV });

  for (const provider of SECRET_PROVIDERS) {
    describe(`Given a premise project (${provider.name})`, { tags: provider.tags }, () => {
      beforeEach(async () => {
        if (provider.liveOnly && !isLiveOrRecordMode()) return;
        await setupSecretsWorkspace(harness(), provider.name);
      });

      it.skipIf(provider.liveOnly && !isLiveOrRecordMode())(
        "It creates 0600 git-ignored .env.local based on fnox config",
        { tags: provider.liveOnly ? [] : ["smoke"] },
        async () => {
          // Arrange
          const h = harness();
          const destination = join(h.workspace, "apps/web/.env.local");

          // Act & Assert for every supported environment
          for (const env of provider.environments) {
            const args = ["secrets", "pull", "--project", "web", "--env", env];
            if (env === "preview") args.push("--branch", "feature/secrets");
            await h.pm(args);

            const expectedValue =
              provider.name === "gcp-secret-manager"
                ? "premise-e2e-gcp-secret-value"
                : `${env}-example`;
            expect(await readFile(destination, "utf8")).toContain(`APP_MESSAGE="${expectedValue}"`);
            expect((await stat(destination)).mode & 0o777).toBe(0o600);
            await h.git(["check-ignore", "--quiet", "--", destination]);
          }

          if (provider.name === "gcp-secret-manager") {
            h.updateRemoteManifest({ gcpProject: resolveLiveGcpConfig().project });
          }
          await h.writeManifest();
        },
      );

      it.skipIf(provider.liveOnly && !isLiveOrRecordMode())(
        "It redacts secret values from output and passes vars to pm run",
        async () => {
          // Arrange
          const h = harness();
          const env = provider.name === "gcp-secret-manager" ? "dev" : "stage";
          const expectedValue =
            provider.name === "gcp-secret-manager"
              ? "premise-e2e-gcp-secret-value"
              : "stage-example";

          // Act
          const pull = await h.pm(["secrets", "pull", "--project", "web", "--env", env]);
          const taskOutput = await h.pm(["run", "web:show-message"]);

          // Assert
          expect(pull.combined).not.toContain(expectedValue);
          expect(taskOutput.stdout.trim()).toBe(expectedValue);
          await h.writeManifest();
        },
      );

      it.skipIf(provider.liveOnly && !isLiveOrRecordMode())(
        "It errors out and preserves the prior .env.local when called for an invalid env",
        async () => {
          // Arrange
          const h = harness();
          const destination = join(h.workspace, "apps/web/.env.local");
          await h.pm(["secrets", "pull", "--project", "web", "--env", "dev"]);
          const before = await readFile(destination, "utf8");

          const configPath = join(h.workspace, "apps/web/fnox.toml");
          const brokenConfig = (await readFile(configPath, "utf8")).replace(
            "[profiles.stage.secrets]",
            "[profiles.unavailable.secrets]",
          );
          await writeFile(configPath, brokenConfig, { mode: 0o600 });
          await chmod(configPath, 0o600);

          // Act
          const failure = await h.pmFail(["secrets", "pull", "--project", "web", "--env", "stage"]);

          // Assert
          expect(failure.code).not.toBe(0);
          expect(await readFile(destination, "utf8")).toBe(before);
          await h.writeManifest();
        },
      );
    });
  }
});

// Private helpers ------------------------------------------------------------

async function setupSecretsWorkspace(
  h: PremiseHarness,
  provider: "local-json" | "gcp-secret-manager",
): Promise<void> {
  await copyScaffold(join(TESTDATA_ROOT, "pulumi/client"), h.workspace);
  await copyScaffold(join(TESTDATA_ROOT, "secrets"), join(h.workspace, "apps/web"));
  if (provider === "gcp-secret-manager") {
    const { project } = resolveLiveGcpConfig();
    await h.writeFile(
      "apps/web/fnox.toml",
      `[providers.gcp]
type = "gcp-sm"
project = "${project}"

[profiles.dev.secrets]
APP_CONFIG = { provider = "gcp", value = "premise-e2e-test-secret", as_file = true }

[profiles.stage.secrets]
APP_CONFIG = { provider = "gcp", value = "premise-e2e-test-secret", as_file = true }
`,
      0o600,
    );
  }
  await h.git(["init", "--quiet"]);
}
