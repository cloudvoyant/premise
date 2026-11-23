import { chmod, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { usePremiseHarness } from "../common/fixtures.js";
import { TESTDATA_ROOT, type PremiseHarness } from "../common/premise-harness.js";
import { copyScaffold, isLiveOrRecordMode } from "../common/test-utils.js";
import { resolveLiveGcpConfig } from "../common/gcp-driver.js";

const CLEAN_CI_ENV: Record<string, string | undefined> = {
  CI: "",
  GITHUB_ACTIONS: "",
  GITLAB_CI: "",
};

async function setupSecretsWorkspace(h: PremiseHarness): Promise<void> {
  await copyScaffold(join(TESTDATA_ROOT, "pulumi/client"), h.workspace);
  await copyScaffold(join(TESTDATA_ROOT, "secrets"), join(h.workspace, "apps/web"));
  await h.git(["init", "--quiet"]);
}

describe("secrets lifecycle", () => {
  const harness = usePremiseHarness({ env: CLEAN_CI_ENV });

  describe("given a workspace with local JSON fnox profiles", () => {
    for (const env of ["dev", "stage", "prod", "preview"] as const) {
      it(
        `when pulling ${env} secrets, then 0600 git-ignored .env.local is written redacted and injected into pm run`,
        { tags: ["e2e", "cli", env === "dev" ? "smoke" : "secrets", "secrets", "fnox", "replay"] },
        async () => {
          // Arrange
          const h = harness();
          await setupSecretsWorkspace(h);
          const destination = join(h.workspace, "apps/web/.env.local");
          const args = ["secrets", "pull", "--project", "web", "--env", env];
          if (env === "preview") args.push("--branch", "feature/secrets");

          // Act
          const pull = await h.pm(args);

          // Assert profile selection, 0600 git-ignored file, redacted output, and Mise task injection
          const envContent = await readFile(destination, "utf8");
          expect(envContent).toContain(`APP_MESSAGE="${env}-example"`);
          const info = await stat(destination);
          expect(info.mode & 0o777).toBe(0o600);
          await h.git(["check-ignore", "--quiet", "--", destination]);
          expect(pull.combined).not.toContain(`${env}-example`);

          const taskOutput = await h.pm(["run", "web:show-message"]);
          expect(taskOutput.stdout.trim()).toBe(`${env}-example`);
          await h.writeManifest();
        },
      );
    }

    it(
      "when the selected profile is missing, then the previous .env.local is preserved byte-for-byte",
      { tags: ["e2e", "cli", "secrets", "fnox", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await setupSecretsWorkspace(h);
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

  describe("given a workspace configured for GCP Secret Manager", () => {
    it.skipIf(!isLiveOrRecordMode())(
      "when pulling dev secrets from dedicated cloudvoyant-premise Secret Manager, then values resolve safely without leaking",
      { tags: ["e2e", "cli", "secrets", "gcp", "fnox", "live"] },
      async () => {
        // Arrange
        const h = harness();
        const { project: gcpProject } = resolveLiveGcpConfig();

        // Act: verify the dedicated cloudvoyant-premise fixture secret is readable via gcloud
        const result = await h.run(
          "gcloud",
          [
            "secrets",
            "versions",
            "access",
            "latest",
            "--secret=premise-e2e-test-secret",
            `--project=${gcpProject}`,
          ],
          { expectSuccess: true },
        );

        // Assert
        expect(result.stdout.trim()).toBe("premise-e2e-gcp-secret-value");
        h.updateRemoteManifest({ gcpProject });
        await h.writeManifest();
      },
    );
  });
});
