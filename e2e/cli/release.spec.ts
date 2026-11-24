/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag release
 */
import { chmod, mkdir, readdir, readFile, realpath, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { HttpCassetteProxy } from "../../libs/testutils/cassette-proxy.js";
import { usePremiseHarness } from "../../libs/testutils/fixtures.js";
import { isReplayMode } from "../../libs/testutils/mode.js";
import {
  RECORDINGS_ROOT,
  REPO_ROOT,
  TESTDATA_ROOT,
  type PremiseHarness,
} from "../../libs/testutils/premise-harness.js";
import { copyScaffold, pathExists } from "../../libs/tsutils/fs.js";
import { assertPrivateFixtureRepo, fixtureGithubRepo } from "../../libs/tsutils/github.js";

// Fixtures -------------------------------------------------------------------

const SOURCE_ONLY_FIXTURE = join(TESTDATA_ROOT, "workspaces/source-only");

const ACTION_TAG_SCENARIOS = [
  {
    name: "advances an existing v<major> tag on stable releases",
    version: "v0.6.0",
    existing: "true",
    failure: "",
    want: "api --method PATCH repos/cloudvoyant/premise/git/refs/tags/v0 -f sha=0123456789012345678901234567890123456789 -F force=true",
    fails: false,
  },
  {
    name: "creates a new v<major> tag on the first release",
    version: "v1.0.0",
    existing: "false",
    failure: "",
    want: "api --method POST repos/cloudvoyant/premise/git/refs -f ref=refs/tags/v1 -f sha=0123456789012345678901234567890123456789",
    fails: false,
  },
  {
    name: "rejects RC versions from updating major tags",
    version: "v0.6.0-rc.1",
    existing: "",
    failure: "",
    want: "",
    fails: true,
  },
  {
    name: "stops safely when the release tag lookup fails",
    version: "v0.6.0",
    existing: "",
    failure: "release",
    want: "",
    fails: true,
  },
  {
    name: "reports failure when updating the remote major tag fails",
    version: "v0.6.0",
    existing: "true",
    failure: "update",
    want: "api --method PATCH repos/cloudvoyant/premise/git/refs/tags/v0",
    fails: true,
  },
] as const;

// Tests ----------------------------------------------------------------------

describe("premise release", { tags: ["e2e", "cli", "release"] }, () => {
  const harness = usePremiseHarness();

  describe("Given a source-only project", { tags: ["github", "replay"] }, () => {
    let remote: string;
    let shimEnv: Record<string, string>;
    let captureFile: string;

    beforeEach(async () => {
      const h = harness();
      await copyScaffold(SOURCE_ONLY_FIXTURE, h.workspace);
      await h.initGit("feat: add Lua plugin");
      remote = join(h.outputDir, "origin.git");
      await h.git(["init", "--bare", "-q", remote], { cwd: h.outputDir });
      await h.git(["remote", "add", "origin", remote]);
      await h.git(["push", "-q", "origin", "HEAD"]);

      const shimBin = await installSourceReleaseShims(h);
      captureFile = join(h.outputDir, "release-capture.log");
      shimEnv = {
        PATH: `${shimBin}:${process.env.PATH ?? ""}`,
        GITHUB_TOKEN: "test-token",
        GH_TOKEN: "test-alias",
        NODE_AUTH_TOKEN: "registry-secret",
        CRATES_TOKEN: "registry-secret",
        RELEASE_CAPTURE: captureFile,
        EXPECTED_RELEASE_ROOT: await realpath(h.workspace),
      };
    });

    it(
      "It plans a stable release on --dry-run and skips artifact builds without creating tags",
      { tags: ["smoke"] },
      async () => {
        // Arrange
        const h = harness();

        // Act
        const plan = await h.pm(["release", "--dry-run"], { env: shimEnv });
        const build = await h.pm(["release", "--build"], {
          env: { ...shimEnv, GITHUB_TOKEN: "", GH_TOKEN: "" },
        });

        // Assert
        expect(plan.stdout).toContain("version=0.1.0\nshould_publish=true");
        expect(build.combined).toContain("skip: workspace has no downloadable release artifacts");
        expect((await h.git(["tag"], { cwd: remote })).stdout.trim()).toBe("");
        await h.writeManifest();
      },
    );

    it("It pushes the stable tag, recovers cleanly on retry after GoReleaser failure, and skips language packages", async () => {
      // Arrange
      const h = harness();

      // Act: first publish fails after pushing tag
      const failed = await h.pmFail(
        ["release", "publish", "--channel", "stable", "--expected-version", "v0.1.0"],
        { env: { ...shimEnv, FAIL_PUBLICATION: "true" } },
      );
      const head = (await h.git(["rev-parse", "HEAD"])).stdout.trim();
      const remoteTag = (
        await h.git(["rev-parse", "refs/tags/v0.1.0"], { cwd: remote })
      ).stdout.trim();

      // Act: retry succeeds idempotently and skips package hooks
      await h.pm(["release", "publish", "--channel", "stable", "--expected-version", "v0.1.0"], {
        env: shimEnv,
      });
      const packages = await h.pm(
        ["release", "packages", "--channel", "stable", "--expected-version", "v0.1.0"],
        { env: shimEnv },
      );
      await h.pm(["release"], { env: shimEnv });

      // Assert
      expect(failed.code).not.toBe(0);
      expect(failed.combined).toContain("run GoReleaser");
      expect(remoteTag).toBe(head);
      expect(packages.combined).toContain(
        "skip: workspace package managers do not publish language packages",
      );
      expect((await readFile(captureFile, "utf8")).split("publish\n").length - 1).toBe(3);
      await h.writeManifest();
    });

    it("It publishes RC releases on [publish-rc] commits and rejects tags pointing away from HEAD", async () => {
      // Arrange
      const h = harness();
      await h.pm(["release", "publish", "--channel", "stable", "--expected-version", "v0.1.0"], {
        env: shimEnv,
      });
      await h.git(["commit", "--allow-empty", "-qm", "fix: next release [publish-rc]"]);
      const rc = (
        await h.pm(["version", "rc", "--identifier", "42"], { env: shimEnv })
      ).stdout.trim();

      // Act: publish RC twice idempotently, then commit and re-attempt same RC tag
      await h.pm(["release", "publish", "--channel", "rc", "--expected-version", rc], {
        env: shimEnv,
      });
      await h.pm(["release", "publish", "--channel", "rc", "--expected-version", rc], {
        env: shimEnv,
      });
      const rcTag = (await h.git(["rev-parse", `refs/tags/${rc}`], { cwd: remote })).stdout.trim();
      const rcHead = (await h.git(["rev-parse", "HEAD"])).stdout.trim();

      await h.git(["commit", "--allow-empty", "-qm", "fix: another change"]);
      const conflict = await h.pmFail(
        ["release", "publish", "--channel", "rc", "--expected-version", rc],
        { env: shimEnv },
      );

      // Assert
      expect(rc).toBe("v0.1.0-rc.42");
      expect(rcTag).toBe(rcHead);
      expect(conflict.code).not.toBe(0);
      expect(conflict.combined).toContain("exists away from HEAD");
      await h.writeManifest();
    });

    it("It executes the root GitHub Action release steps for stable and RC channels", async () => {
      // Arrange
      const h = harness();
      const scripts = await loadActionScripts();
      const ghOutput = join(h.outputDir, "github-output.txt");
      await writeFile(ghOutput, "", "utf8");

      // Act: stable plan + publish via action.yml scripts
      await runActionStep(h, scripts["Plan stable release"], "plan", "stable", "", shimEnv, {
        GITHUB_OUTPUT: ghOutput,
      });
      await runActionStep(h, scripts["Plan runner schedule"], "plan", "stable", "", shimEnv, {
        GITHUB_OUTPUT: ghOutput,
      });
      await runActionStep(
        h,
        scripts["Run Premise operation"],
        "release",
        "stable",
        "v0.1.0",
        shimEnv,
      );
      const pkgOut = await runActionStep(
        h,
        scripts["Run Premise operation"],
        "packages",
        "stable",
        "v0.1.0",
        shimEnv,
      );

      // Assert stable outputs
      const stableOutput = await readFile(ghOutput, "utf8");
      expect(stableOutput).toContain("should_publish=true");
      expect(stableOutput).toContain("native=false");
      expect(pkgOut.combined).toContain(
        "skip: workspace package managers do not publish language packages",
      );

      // Act: RC plan (unmarked vs marked)
      await h.git(["commit", "--allow-empty", "-qm", "fix: rc via action [publish-rc]"]);
      const rc = (
        await h.pm(["version", "rc", "--identifier", "42"], { env: shimEnv })
      ).stdout.trim();
      await writeFile(ghOutput, "", "utf8");
      await runActionStep(h, scripts["Plan release candidate"], "plan", "rc", rc, shimEnv, {
        GITHUB_OUTPUT: ghOutput,
        MARKED_PUSH: "false",
        GITHUB_RUN_NUMBER: "42",
      });
      expect(await readFile(ghOutput, "utf8")).toContain("should_publish_rc=false");

      await writeFile(ghOutput, "", "utf8");
      await runActionStep(h, scripts["Plan release candidate"], "plan", "rc", rc, shimEnv, {
        GITHUB_OUTPUT: ghOutput,
        MARKED_PUSH: "true",
        GITHUB_RUN_NUMBER: "42",
      });
      expect(await readFile(ghOutput, "utf8")).toContain("should_publish_rc=true");
      await h.writeManifest();
    });

    it.skipIf(!process.env.PREMISE_TEST_GORELEASER)(
      "It passes pinned GoReleaser 2.18.1 check and snapshot without checksums or tags",
      async () => {
        // Arrange
        const h = harness();
        const goreleaserBin = process.env.PREMISE_TEST_GORELEASER!;
        expect(
          (await h.run(goreleaserBin, ["--version"], { expectSuccess: true })).combined,
        ).toContain("2.18.1");
        const configPath = join(h.outputDir, "goreleaser.yml");
        const rendered = await h.pm(["release", "config"]);
        await writeFile(configPath, rendered.stdout, { mode: 0o600 });

        // Act
        await h.run(goreleaserBin, ["check", "-f", configPath], { expectSuccess: true });
        await h.run(goreleaserBin, ["release", "--snapshot", "--clean", "-f", configPath], {
          expectSuccess: true,
          env: { GITHUB_TOKEN: "", GH_TOKEN: "" },
        });

        // Assert
        const distEntries = (await pathExists(join(h.workspace, "dist")))
          ? await readdir(join(h.workspace, "dist"))
          : [];
        expect(distEntries.filter((f) => f.includes("checksums"))).toEqual([]);
        expect((await h.git(["tag"])).stdout.trim()).toBe("");
        await h.writeManifest();
      },
    );
  });

  describe(
    "Given a repository with GitHub Action major tags",
    { tags: ["github", "replay"] },
    () => {
      beforeEach(async () => {
        const h = harness();
        await copyScaffold(join(TESTDATA_ROOT, "action-publication"), h.workspace);
        await chmod(join(h.workspace, "gh"), 0o755);
      });

      for (const s of ACTION_TAG_SCENARIOS) {
        it(`It ${s.name}`, async () => {
          // Arrange
          const h = harness();
          const callsFile = join(h.workspace, "calls");

          // Act
          const result = await h.run("mise", ["run", "publish:update-action-tags"], {
            cwd: REPO_ROOT,
            expectSuccess: !s.fails,
            env: {
              PATH: `${h.workspace}:${process.env.PATH ?? ""}`,
              RELEASE_VERSION: s.version,
              PREMISE_TEST_GH_CALLS: callsFile,
              PREMISE_TEST_MAJOR_EXISTS: s.existing,
              PREMISE_TEST_GH_FAILURE: s.failure,
            },
          });

          // Assert
          if (s.fails) expect(result.code).not.toBe(0);
          else expect(result.code).toBe(0);
          if (s.version.includes("-rc.")) {
            expect(await pathExists(callsFile)).toBe(false);
          } else if (s.want) {
            expect(await readFile(callsFile, "utf8")).toContain(s.want);
          } else {
            expect(await readFile(callsFile, "utf8")).not.toContain("--method");
          }
          await h.writeManifest();
        });
      }
    },
  );

  describe(
    "Given a private GitHub fixture repository (cloudvoyant/premise-e2e)",
    { tags: ["github", "replay", "live"] },
    () => {
      it("It enforces private visibility before writing via the HTTP cassette proxy", async () => {
        // Arrange
        const h = harness();
        const cassettePath = join(RECORDINGS_ROOT, "cli/release-github-fixture-visibility.json");
        const repo = fixtureGithubRepo();
        if (isReplayMode() && !(await pathExists(cassettePath))) {
          throw new Error(
            `Missing replay cassette ${cassettePath}. Run "mise run e2e:record -- release.spec.ts" locally to record it.`,
          );
        }

        const ghToken = isReplayMode()
          ? ""
          : (await h.run("gh", ["auth", "token"], { expectSuccess: true })).stdout.trim();
        const proxy = new HttpCassetteProxy({
          mode: h.mode,
          provider: "github",
          cassettePath,
          journeyKey: "github-fixture-visibility",
          upstreamBaseUrl: "https://api.github.com",
          upstreamHeaders: isReplayMode()
            ? {}
            : {
                Authorization: `Bearer ${ghToken}`,
                "User-Agent": "premise-e2e",
                Accept: "application/vnd.github+json",
              },
          forbiddenSecrets: ghToken ? [ghToken] : [],
          runId: h.runId,
          repository: repo,
        });
        await proxy.start();

        try {
          // Act
          const res = await fetch(`${proxy.url}/repos/${repo}`);
          const body = (await res.json()) as { full_name: string; private: boolean };

          // Assert
          expect(res.status).toBe(200);
          expect(body.full_name).toBe(repo);
          expect(body.private).toBe(true);

          if (!isReplayMode()) {
            await assertPrivateFixtureRepo(repo);
            h.updateRemoteManifest({ githubRepo: repo });
          }
          await h.writeManifest();
        } finally {
          await proxy.stop();
        }
      });
    },
  );

  describe("Given a project with publishable packages", { tags: ["known-bug"] }, () => {
    it.skip(
      "It passes without registry tokens when all projects are local-only [CLO-228]",
      { tags: ["cargo", "replay"] },
      async () => {
        const h = harness();
        await copyScaffold(join(TESTDATA_ROOT, "workspaces/cargo"), h.workspace);
        await h.writeManifest();
      },
    );

    it.skip(
      "It rejects a publishable package depending on a local-only sibling before tagging [CLO-231]",
      { tags: ["bun", "cargo", "replay"] },
      async () => {
        const h = harness();
        await copyScaffold(join(TESTDATA_ROOT, "workspaces/bun"), h.workspace);
        await h.writeManifest();
      },
    );

    it.skip(
      "It publishes GitHub Release archives and checksums and runs them in a clean consumer [CLO-228, CLO-230, CLO-235]",
      { tags: ["github", "go", "cargo", "bun", "live"] },
      async () => {
        const h = harness();
        await copyScaffold(join(TESTDATA_ROOT, "workspaces/go"), h.workspace);
        await h.writeManifest();
      },
    );

    it.skip(
      "It publishes private Artifact Registry packages in dependency order without a GitHub Release [CLO-229, CLO-231..CLO-235]",
      { tags: ["gcp", "go", "cargo", "bun", "live"] },
      async () => {
        const h = harness();
        await copyScaffold(join(TESTDATA_ROOT, "workspaces/bun"), h.workspace);
        await h.writeManifest();
      },
    );

    it.skip(
      "It publishes both GitHub Releases and Artifact Registry packages and records proofs in artifacts.json [CLO-228..CLO-236]",
      { tags: ["github", "gcp", "go", "cargo", "bun", "live"] },
      async () => {
        const h = harness();
        await copyScaffold(join(TESTDATA_ROOT, "workspaces/go"), h.workspace);
        await h.writeManifest();
      },
    );
  });
});

// Private helpers ------------------------------------------------------------

async function installSourceReleaseShims(h: PremiseHarness): Promise<string> {
  const shimBin = join(h.outputDir, "shim-bin");
  await mkdir(shimBin, { recursive: true });
  await writeFile(join(shimBin, "pm"), `#!/bin/sh\nexec "${h.binary}" "$@"\n`, { mode: 0o755 });
  await chmod(join(shimBin, "pm"), 0o755);
  await writeFile(
    join(shimBin, "mise"),
    `#!/bin/sh
set -eu
[ -z "\${GITHUB_TOKEN:-}" ]
[ -z "\${GH_TOKEN:-}" ]
[ -z "\${NODE_AUTH_TOKEN:-}" ]
[ -z "\${CRATES_TOKEN:-}" ]
case "$*" in
 exec\\ goreleaser@2.18.1\\ --\\ env\\ -0) /usr/bin/env -0 ;;
 *) echo "unexpected mise invocation: $*" >&2; exit 1 ;;
esac
`,
    { mode: 0o755 },
  );
  await chmod(join(shimBin, "mise"), 0o755);
  await writeFile(
    join(shimBin, "goreleaser"),
    `#!/bin/sh
set -eu
[ -n "\${GITHUB_TOKEN:-}" ]
[ -z "\${GH_TOKEN:-}" ]
[ -z "\${NODE_AUTH_TOKEN:-}" ]
[ -z "\${CRATES_TOKEN:-}" ]
[ "$1" = release ]
while [ "$#" -gt 0 ]; do
 case "$1" in
  -f) shift; configuration=$1 ;;
  --snapshot) echo "source-only build should skip artifact tools" >&2; exit 1 ;;
 esac
 shift
done
[ "$(pwd -P)" = "$EXPECTED_RELEASE_ROOT" ]
grep -q 'skip: true' "$configuration"
grep -q 'disable: true' "$configuration"
! grep -q 'extra_files:' "$configuration"
printf 'publish\\n' >> "$RELEASE_CAPTURE"
[ "\${FAIL_PUBLICATION:-false}" != true ]
`,
    { mode: 0o755 },
  );
  await chmod(join(shimBin, "goreleaser"), 0o755);
  return shimBin;
}

async function runActionStep(
  h: PremiseHarness,
  script: string,
  operation: string,
  channel: string,
  version: string,
  baseEnv: Record<string, string>,
  extraEnv: Record<string, string> = {},
) {
  return h.run("bash", ["-c", script], {
    expectSuccess: true,
    env: {
      ...baseEnv,
      COMMAND: operation,
      FLOW: channel === "rc" ? "on-commit" : "on-merge",
      RELEASE_MODE: "auto",
      CHANNEL: channel,
      VERSION: version,
      DEPLOY_ENVIRONMENT: "",
      PREVIEW_BRANCH: "",
      PROJECT: "",
      PLATFORM: "",
      OUTPUT_DIR: "",
      FILES_DIR: "",
      ROOT_ONLY: "",
      SKIP_ROOT: "",
      ...extraEnv,
    },
  });
}

async function loadActionScripts(): Promise<Record<string, string>> {
  const raw = await readFile(join(REPO_ROOT, "action.yml"), "utf8");
  const lines = raw.split("\n");
  const scripts: Record<string, string> = {};
  let currentName = "";
  let collectingRun = false;
  let runIndent = 0;
  let runLines: string[] = [];

  const flush = () => {
    if (collectingRun && currentName) {
      scripts[currentName] = `${runLines.join("\n")}\n`;
    }
    collectingRun = false;
    runLines = [];
  };

  for (const line of lines) {
    if (collectingRun) {
      if (line.trim() === "") {
        runLines.push("");
        continue;
      }
      const indent = line.length - line.trimStart().length;
      if (indent >= runIndent) {
        runLines.push(line.slice(runIndent));
        continue;
      }
      flush();
    }
    const nameMatch = line.match(/^\s+-\s+name:\s*(.+)$/);
    if (nameMatch) {
      currentName = nameMatch[1].trim().replace(/^["']|["']$/g, "");
      continue;
    }
    const runMatch = line.match(/^(\s+)run:\s*\|$/);
    if (runMatch && currentName) {
      collectingRun = true;
      runIndent = runMatch[1].length + 2;
      runLines = [];
    }
  }
  flush();
  return scripts;
}
