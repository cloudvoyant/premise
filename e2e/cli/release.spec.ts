import { chmod, mkdir, readFile, realpath, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { usePremiseHarness } from "../common/fixtures.js";
import { HttpCassetteProxy } from "../common/cassette-proxy.js";
import { assertPrivateFixtureRepo, fixtureGithubRepo } from "../common/github-driver.js";
import {
  RECORDINGS_ROOT,
  REPO_ROOT,
  TESTDATA_ROOT,
  type PremiseHarness,
} from "../common/premise-harness.js";
import { copyScaffold, isReplayMode, pathExists } from "../common/test-utils.js";

/** Extract named step `run:` scripts from root action.yml without external YAML dependencies. */
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

async function createSourceReleaseFixture(h: PremiseHarness, subdir: string) {
  const workspace = join(h.workspace, subdir);
  const remote = join(h.outputDir, `${subdir}-origin.git`);
  await mkdir(join(workspace, "libs/lua-plugin/lua"), { recursive: true });
  await writeFile(
    join(workspace, "premise.yaml"),
    `workspace:
  name: lua-plugin
  kind: monorepo
  schema-version: "0.2"
  package_managers: []
  providers: {ci: github}
  projects:
    - name: lua-plugin
      template: cloudvoyant/premise:premise-lib
      version: 0.1.0
      path: libs/lua-plugin
`,
    "utf8",
  );
  await writeFile(join(workspace, "libs/lua-plugin/lua/init.lua"), "return {}\n", "utf8");
  await writeFile(
    join(workspace, "mise.toml"),
    "[tasks.publish]\nrun = 'exit 1'\n[tasks.\"publish:rc\"]\nrun = 'exit 1'\n",
    "utf8",
  );
  await h.git(["init", "-q", "--initial-branch=main"], { cwd: workspace });
  await h.git(["config", "user.email", "test@example.invalid"], { cwd: workspace });
  await h.git(["config", "user.name", "Test"], { cwd: workspace });
  await h.git(["add", "."], { cwd: workspace });
  await h.git(["commit", "-qm", "feat: add Lua plugin"], { cwd: workspace });
  await h.git(["init", "--bare", "-q", remote], { cwd: h.outputDir });
  await h.git(["remote", "add", "origin", remote], { cwd: workspace });
  await h.git(["push", "-q", "origin", "HEAD"], { cwd: workspace });
  return { workspace, remote };
}

describe("release lifecycle", () => {
  const harness = usePremiseHarness();

  describe("given a source-only workspace with failing publish traps", () => {
    it(
      "when releasing via split-cli, full-cli, and action steps, then dry-run, build skip, tag retry, RC, and tag-conflict guards all hold",
      { tags: ["e2e", "cli", "smoke", "release", "github", "replay"] },
      async () => {
        // Arrange: install shim binaries for mise and goreleaser that enforce secret isolation and source-only config
        const h = harness();
        const shimBin = join(h.outputDir, "shim-bin");
        await mkdir(shimBin, { recursive: true });
        await writeFile(join(shimBin, "pm"), `#!/bin/sh\nexec "${h.binary}" "$@"\n`, {
          mode: 0o755,
        });
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

        const captureFile = join(h.outputDir, "release-capture.log");
        const scripts = await loadActionScripts();
        for (const required of [
          "Plan stable release",
          "Plan release candidate",
          "Plan runner schedule",
          "Run Premise operation",
        ]) {
          expect(scripts[required]).toBeTruthy();
        }

        const baseEnv = {
          PATH: `${shimBin}:${process.env.PATH ?? ""}`,
          GITHUB_TOKEN: "test-token",
          GH_TOKEN: "test-alias",
          NODE_AUTH_TOKEN: "registry-secret",
          CRATES_TOKEN: "registry-secret",
          RELEASE_CAPTURE: captureFile,
        };

        // Act & Assert across split-cli, full-cli, and action modes
        for (const mode of ["split-cli", "full-cli", "action"] as const) {
          const { workspace, remote } = await createSourceReleaseFixture(h, mode);
          const physical = await realpath(workspace);
          const modeEnv = { ...baseEnv, EXPECTED_RELEASE_ROOT: physical };

          const plan = await h.pm(["release", "--dry-run"], { cwd: workspace, env: modeEnv });
          expect(plan.stdout).toContain("version=0.1.0\nshould_publish=true");

          const buildOut = await h.pm(["release", "--build"], {
            cwd: workspace,
            env: { ...modeEnv, GITHUB_TOKEN: "", GH_TOKEN: "" },
          });
          expect(buildOut.combined).toContain(
            "skip: workspace has no downloadable release artifacts",
          );
          expect((await h.git(["tag"], { cwd: remote })).stdout.trim()).toBe("");

          const runAction = (
            scriptName: string,
            operation: string,
            channel: string,
            version: string,
            extraEnv: Record<string, string> = {},
          ) =>
            h.run("bash", ["-c", scripts[scriptName]], {
              cwd: workspace,
              expectSuccess: true,
              env: {
                ...modeEnv,
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

          const publish = async (channel: string, version: string) => {
            if (mode === "action") {
              await runAction("Run Premise operation", "release", channel, version);
              return;
            }
            if (mode === "full-cli" && channel === "stable") {
              await h.pm(["release"], { cwd: workspace, env: modeEnv });
              return;
            }
            await h.pm(
              ["release", "publish", "--channel", channel, "--expected-version", version],
              { cwd: workspace, env: modeEnv },
            );
          };

          const packages = async (channel: string, version: string) => {
            const res =
              mode === "action"
                ? await runAction("Run Premise operation", "packages", channel, version)
                : await h.pm(
                    ["release", "packages", "--channel", channel, "--expected-version", version],
                    { cwd: workspace, env: modeEnv },
                  );
            expect(res.combined).toContain(
              "skip: workspace package managers do not publish language packages",
            );
          };

          const ghOutput = join(h.outputDir, `github-output-${mode}.txt`);
          if (mode === "action") {
            await writeFile(ghOutput, "", "utf8");
            await runAction("Plan stable release", "plan", "stable", "", {
              GITHUB_OUTPUT: ghOutput,
            });
            await runAction("Plan runner schedule", "plan", "stable", "", {
              GITHUB_OUTPUT: ghOutput,
            });
            const outContent = await readFile(ghOutput, "utf8");
            expect(outContent).toContain("should_publish=true");
            expect(outContent).toContain("native=false");
          }

          // First publication fails, leaving tag pushed for safe retry
          const failArgs =
            mode === "full-cli"
              ? ["release"]
              : ["release", "publish", "--channel", "stable", "--expected-version", "v0.1.0"];
          const failedPub = await h.pmFail(failArgs, {
            cwd: workspace,
            env: { ...modeEnv, FAIL_PUBLICATION: "true" },
          });
          expect(failedPub.code).not.toBe(0);
          expect(failedPub.combined).toContain("run GoReleaser");
          const head = (await h.git(["rev-parse", "HEAD"], { cwd: workspace })).stdout.trim();
          const remoteTag = (
            await h.git(["rev-parse", "refs/tags/v0.1.0"], { cwd: remote })
          ).stdout.trim();
          expect(remoteTag).toBe(head);

          // Retry succeeds and is idempotent
          await publish("stable", "v0.1.0");
          await packages("stable", "v0.1.0");
          await publish("stable", "v0.1.0");

          // RC channel
          await h.git(["commit", "--allow-empty", "-qm", "fix: next release [publish-rc]"], {
            cwd: workspace,
          });
          const rc = (
            await h.pm(["version", "rc", "--identifier", "42"], { cwd: workspace, env: modeEnv })
          ).stdout.trim();
          expect(rc).toBe("v0.1.0-rc.42");

          if (mode === "action") {
            await writeFile(ghOutput, "", "utf8");
            await runAction("Plan release candidate", "plan", "rc", rc, {
              GITHUB_OUTPUT: ghOutput,
              MARKED_PUSH: "false",
              GITHUB_RUN_NUMBER: "42",
            });
            expect(await readFile(ghOutput, "utf8")).toContain("should_publish_rc=false");

            await writeFile(ghOutput, "", "utf8");
            await runAction("Plan release candidate", "plan", "rc", rc, {
              GITHUB_OUTPUT: ghOutput,
              MARKED_PUSH: "true",
              GITHUB_RUN_NUMBER: "42",
            });
            expect(await readFile(ghOutput, "utf8")).toContain("should_publish_rc=true");
          }

          await publish("rc", rc);
          await packages("rc", rc);
          await publish("rc", rc);
          const rcRemoteTag = (
            await h.git(["rev-parse", `refs/tags/${rc}`], { cwd: remote })
          ).stdout.trim();
          const rcHead = (await h.git(["rev-parse", "HEAD"], { cwd: workspace })).stdout.trim();
          expect(rcRemoteTag).toBe(rcHead);

          // Existing RC tag away from HEAD must fail
          await h.git(["commit", "--allow-empty", "-qm", "fix: another change"], {
            cwd: workspace,
          });
          const conflict = await h.pmFail(
            ["release", "publish", "--channel", "rc", "--expected-version", rc],
            { cwd: workspace, env: modeEnv },
          );
          expect(conflict.code).not.toBe(0);
          expect(conflict.combined).toContain("exists away from HEAD");
        }

        const captured = await readFile(captureFile, "utf8");
        expect(captured.split("publish\n").length - 1).toBe(15);
        await h.writeManifest();
      },
    );
  });

  describe("given a repository with major GitHub Action tags", () => {
    const scenarios = [
      {
        name: "advances an existing major tag on a stable release",
        version: "v0.6.0",
        existing: "true",
        failure: "",
        want: "api --method PATCH repos/cloudvoyant/premise/git/refs/tags/v0 -f sha=0123456789012345678901234567890123456789 -F force=true",
        fails: false,
      },
      {
        name: "creates a new major tag on the first release",
        version: "v1.0.0",
        existing: "false",
        failure: "",
        want: "api --method POST repos/cloudvoyant/premise/git/refs -f ref=refs/tags/v1 -f sha=0123456789012345678901234567890123456789",
        fails: false,
      },
      {
        name: "rejects an RC release attempting to update a major tag",
        version: "v0.6.0-rc.1",
        existing: "",
        failure: "",
        want: "",
        fails: true,
      },
      {
        name: "fails safely when the release tag cannot be read",
        version: "v0.6.0",
        existing: "",
        failure: "release",
        want: "",
        fails: true,
      },
      {
        name: "fails safely when updating the remote major tag fails",
        version: "v0.6.0",
        existing: "true",
        failure: "update",
        want: "api --method PATCH repos/cloudvoyant/premise/git/refs/tags/v0",
        fails: true,
      },
    ];

    for (const s of scenarios) {
      it(
        `when running publish:update-action-tags (${s.name}), then only valid stable major refs update`,
        { tags: ["e2e", "cli", "release", "github", "replay"] },
        async () => {
          // Arrange
          const h = harness();
          await copyScaffold(join(TESTDATA_ROOT, "action-publication"), h.workspace);
          await chmod(join(h.workspace, "gh"), 0o755);
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
          if (s.fails) {
            expect(result.code).not.toBe(0);
          } else {
            expect(result.code).toBe(0);
          }
          if (s.version.includes("-rc.")) {
            expect(await pathExists(callsFile)).toBe(false);
          } else if (s.want) {
            expect(await readFile(callsFile, "utf8")).toContain(s.want);
          } else {
            expect(await readFile(callsFile, "utf8")).not.toContain("--method");
          }
          await h.writeManifest();
        },
      );
    }
  });

  describe("given the private GitHub fixture repository (cloudvoyant/premise-e2e)", () => {
    it(
      "when verifying private visibility guard via HTTP cassette proxy, then replay/record/live enforces private: true",
      { tags: ["e2e", "cli", "release", "github", "replay", "live"] },
      async () => {
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
      },
    );
  });

  describe("given a checked client monorepo (release destination permutations)", () => {
    it.skip(
      "when releasing local-only libs + internal app without registry tokens, then checks pass with zero releases or registry writes [CLO-228]",
      { tags: ["e2e", "cli", "release", "cargo", "known-bug", "replay"] },
      async () => {
        // Tracked by CLO-228: generated client on-merge.yml unconditionally fails without CRATES_TOKEN.
        const h = harness();
        await h.pm(["init"]);
        await h.writeManifest();
      },
    );

    it.skip(
      "when a publishable package depends on a local-only sibling lib, then preflight rejects before tagging [CLO-231]",
      { tags: ["e2e", "cli", "release", "bun", "cargo", "known-bug", "replay"] },
      async () => {
        // Tracked by CLO-231: PublicationTargets publishes apps before libs and ignores workspace dependency order.
        const h = harness();
        await h.pm(["init"]);
        await h.writeManifest();
      },
    );

    it.skip(
      "when releasing GitHub Release-only (Go, Cargo, Bun), then multi-OS archives + checksums publish and run in a clean consumer [CLO-228, CLO-230, CLO-235]",
      { tags: ["e2e", "cli", "release", "github", "go", "cargo", "bun", "known-bug", "live"] },
      async () => {
        // Tracked by CLO-228, CLO-230 (Cargo GoReleaser drops binaries), CLO-235 (CI release DAG).
        const h = harness();
        await h.pm(["init"]);
        await h.writeManifest();
      },
    );

    it.skip(
      "when releasing Artifact Registry-only (Bun GAR npm, Go GAR Go/GOPRIVATE, Cargo sparse), then sibling chains publish topologically without a GH Release and install in a clean consumer [CLO-229, CLO-231..CLO-235]",
      { tags: ["e2e", "cli", "release", "gcp", "go", "cargo", "bun", "known-bug", "live"] },
      async () => {
        // Tracked by CLO-229, CLO-231, CLO-232, CLO-233, CLO-234, CLO-235.
        const h = harness();
        await h.pm(["init"]);
        await h.writeManifest();
      },
    );

    it.skip(
      "when releasing Both GH Release + Artifact Registry, then both publish, verify in a clean consumer, and record in artifacts.json [CLO-228..CLO-236]",
      {
        tags: ["e2e", "cli", "release", "github", "gcp", "go", "cargo", "bun", "known-bug", "live"],
      },
      async () => {
        // Tracked by CLO-228..CLO-236.
        const h = harness();
        await h.pm(["init"]);
        await h.writeManifest();
      },
    );
  });
});
