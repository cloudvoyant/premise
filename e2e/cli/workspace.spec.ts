import { chmod, mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { createNativeRegistry, usePremiseHarness } from "../common/fixtures.js";
import { REPO_ROOT, TESTDATA_ROOT, type PremiseHarness } from "../common/premise-harness.js";
import { copyScaffold, pathExists } from "../common/test-utils.js";

const COMMON_CONTRACTS = [
  "install",
  "build",
  "clean",
  "test",
  "lint",
  "lint:fix",
  "format",
  "format:check",
  "env-pull",
  "publish:rc",
  "publish",
];
const APP_ONLY_CONTRACTS = ["run", "dev", "deploy", "e2e"];

async function scaffoldProject(
  h: PremiseHarness,
  kind: "lib" | "app",
  infrastructure: boolean,
): Promise<{ registry: string; projectDir: string }> {
  const registry = join(h.outputDir, "registry");
  await mkdir(registry, { recursive: true });
  await h.pm(["init", "--kind", "template-registry"], { cwd: registry });
  await h.pm(["template", "init", kind], { cwd: registry });
  await copyScaffold(join(TESTDATA_ROOT, "scaffold", kind), join(registry, "templates", kind));

  const initArgs = infrastructure ? ["init", "--infra", "--pulumi-lang", "go"] : ["init"];
  await h.pm(initArgs);
  await h.pm(["generate", `${registry}:${kind}`, "--answer", "name=example"]);
  return {
    registry,
    projectDir: join(h.workspace, kind === "lib" ? "libs" : "apps", "example"),
  };
}

async function assertScaffoldWorkflows(workspace: string, infrastructure: boolean): Promise<void> {
  for (const name of ["on-commit.yml", "on-merge.yml"]) {
    const content = await readFile(join(workspace, ".github/workflows", name), "utf8");
    expect(content).toContain("cloudvoyant/premise@v0\n");
  }
  const onCommit = await readFile(join(workspace, ".github/workflows/on-commit.yml"), "utf8");
  expect(onCommit).toContain(
    "if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name != github.repository\n",
  );

  const deployPath = join(workspace, ".github/workflows/on-deploy.yml");
  if (infrastructure) {
    const deploy = await readFile(deployPath, "utf8");
    expect(deploy).toContain("workflow_dispatch:");
  } else {
    expect(await pathExists(deployPath)).toBe(false);
  }
}

async function assertProjectContracts(projectDir: string, isApp: boolean): Promise<void> {
  const content = await readFile(join(projectDir, "mise.toml"), "utf8");
  for (const task of COMMON_CONTRACTS) {
    const escaped = task.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    expect(content).toMatch(new RegExp(`\\[tasks\\.("?${escaped}"?)\\]`));
  }
  for (const task of APP_ONLY_CONTRACTS) {
    const present = new RegExp(`\\[tasks\\.("?${task}"?)\\]`).test(content);
    expect(present).toBe(isApp);
  }
}

async function assertNoTempDirs(...roots: string[]): Promise<void> {
  const forbidden = [
    ".premise-stage-",
    "premise-selected-",
    "premise-tool-preflight-",
    "premise-candidate-validation-",
  ];
  for (const root of roots) {
    if (!(await pathExists(root))) continue;
    const entries = await readdir(root);
    for (const name of entries) {
      for (const prefix of forbidden) {
        expect(name).not.toContain(prefix);
      }
    }
  }
}

describe("workspace lifecycle", () => {
  const harness = usePremiseHarness();

  describe("given a fresh initialized workspace", () => {
    it(
      "when scaffolding a lib or app without infra, then workflows, substitutions, contracts, and runnable tasks succeed",
      { tags: ["e2e", "cli", "smoke", "workspace", "go", "replay"] },
      async () => {
        // Arrange
        const h = harness();

        // Act: scaffold a library first in workspace
        const { projectDir: libDir } = await scaffoldProject(h, "lib", false);

        // Assert library workflows, substitutions, build/test, contracts, and no infra plan
        await assertScaffoldWorkflows(h.workspace, false);
        expect(await readFile(join(libDir, "go.mod"), "utf8")).toContain(
          "module example.com/example",
        );
        expect(await readFile(join(libDir, "greeting.go"), "utf8")).toContain('return "example"');
        expect(await readFile(join(libDir, "README.md"), "utf8")).toContain(
          "A runnable example project.",
        );
        await h.pm(["run", "example:build"]);
        await h.pm(["run", "example:test"]);
        await assertProjectContracts(libDir, false);
        const plan = await h.pm(["ci", "plan", "--ci-output"]);
        expect(plan.stdout).toContain("has_infra=false\n");

        // Act: scaffold an app without infra in a sibling workspace under outputDir
        const appWorkspace = join(h.outputDir, "app-workspace");
        await mkdir(appWorkspace, { recursive: true });
        const appRegistry = join(h.outputDir, "app-registry");
        await mkdir(appRegistry, { recursive: true });
        await h.pm(["init", "--kind", "template-registry"], { cwd: appRegistry });
        await h.pm(["template", "init", "app"], { cwd: appRegistry });
        await copyScaffold(join(TESTDATA_ROOT, "scaffold/app"), join(appRegistry, "templates/app"));
        await h.pm(["init"], { cwd: appWorkspace });
        await h.pm(["generate", `${appRegistry}:app`, "--answer", "name=example"], {
          cwd: appWorkspace,
        });

        // Assert app workflows, substitutions, runnable output, contracts, and absent infra
        const appDir = join(appWorkspace, "apps/example");
        await assertScaffoldWorkflows(appWorkspace, false);
        expect(await readFile(join(appDir, "go.mod"), "utf8")).toContain(
          "module example.com/example",
        );
        expect(await readFile(join(appDir, "main.go"), "utf8")).toContain('fmt.Println("example")');
        const runResult = await h.pm(["run", "example:run"], { cwd: appWorkspace });
        expect(runResult.stdout.trim()).toBe("example");
        await assertProjectContracts(appDir, true);
        expect(await pathExists(join(appWorkspace, "infra"))).toBe(false);
        expect(await pathExists(join(appWorkspace, ".github/workflows/on-deploy.yml"))).toBe(false);
        await h.writeManifest();
      },
    );

    for (const manager of ["go", "cargo", "bun"] as const) {
      it(
        `when generating a ${manager} lib+app pair, then native tools build, test, and run with preserved e2e/outputs/`,
        { tags: ["e2e", "cli", "workspace", manager, "replay"] },
        async () => {
          // Arrange
          const h = harness();
          const registry = await createNativeRegistry(
            manager,
            join(h.outputDir, `registry-${manager}`),
          );
          await h.pm(["init"]);
          await h.pm([
            "generate",
            `${registry}:lib-template`,
            "--answer",
            "name=helper,library=helper",
          ]);
          const initialRules = await h.readFile(".editorconfig");
          const clientRules = `${initialRules}\n# Client quality rules\n`;
          await h.writeFile(".editorconfig", clientRules, 0o644);

          const lockFile = manager === "cargo" ? "Cargo.lock" : manager === "bun" ? "bun.lock" : "";
          if (lockFile) {
            await h.writeFile(lockFile, "stale lock fixture", 0o640);
            await writeFile(
              join(registry, "templates/app-template", lockFile),
              "stale template lock fixture",
              "utf8",
            );
          }

          // Act
          await h.pm([
            "generate",
            `${registry}:app-template`,
            "--answer",
            "name=tool,library=helper",
          ]);
          const lockExistsAfterGen = lockFile
            ? await pathExists(join(h.workspace, lockFile))
            : true;
          await h.pm(["install"]);

          // Assert quality rules preserved and not copied into subprojects
          const baselineRules = await readFile(join(registry, ".editorconfig"), "utf8");
          expect(initialRules).toBe(baselineRules);
          expect(await h.readFile(".editorconfig")).toBe(clientRules);
          expect(await pathExists(join(h.workspace, "libs/helper/.editorconfig"))).toBe(false);
          expect(await pathExists(join(h.workspace, "apps/tool/.editorconfig"))).toBe(false);

          // Assert lockfile regeneration, permissions, and native build/run
          expect(lockExistsAfterGen).toBe(true);
          if (lockFile) {
            expect(await h.readFile(lockFile)).not.toContain("stale");
            expect(await pathExists(join(h.workspace, "apps/tool", lockFile))).toBe(false);
            const info = await stat(join(h.workspace, lockFile));
            expect(info.mode & 0o777).toBe(0o640);
          }

          const appOutput = await h.pm(["run", "tool:run"]);
          expect(appOutput.stdout.trim()).toBe("hello");

          if (manager === "go") {
            await h.run("go", ["test", "./..."], { expectSuccess: true });
            expect(await pathExists(join(h.workspace, "libs/helper/go.mod"))).toBe(false);
            expect(await pathExists(join(h.workspace, "apps/tool/go.mod"))).toBe(false);
            expect(await pathExists(join(h.workspace, "go.work"))).toBe(false);
          } else if (manager === "cargo") {
            const meta = await h.run(
              "cargo",
              ["metadata", "--no-deps", "--format-version=1", "--offline"],
              { expectSuccess: true },
            );
            const parsed = JSON.parse(meta.stdout) as { workspace_members: string[] };
            expect(parsed.workspace_members).toHaveLength(2);
            await h.run("cargo", ["fetch", "--offline"], { expectSuccess: true });
            expect(await pathExists(join(h.workspace, "Cargo.lock"))).toBe(true);
          } else if (manager === "bun") {
            expect(await pathExists(join(h.workspace, "bun.lock"))).toBe(true);
            await h.run("bun", ["install", "--ignore-scripts", "--frozen-lockfile"], {
              cwd: join(h.workspace, "apps/tool"),
              expectSuccess: true,
            });
            const rootPkg = await h.readFile("package.json");
            expect(rootPkg).not.toContain("registry-development");
            expect(rootPkg).not.toContain("templates/");
          }

          const manifest = await h.readFile("premise.yaml");
          expect(manifest).toContain("name: helper");
          expect(manifest).toContain("name: tool");
          await assertNoTempDirs(join(h.workspace, "apps"), join(h.workspace, "libs"));
          await h.writeManifest();
        },
      );
    }

    it(
      "when combining Go, Cargo, Bun, and language-neutral templates, then all workspaces coexist cleanly",
      { tags: ["e2e", "cli", "workspace", "go", "cargo", "bun", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await h.pm(["init"]);
        const managers = ["go", "bun", "cargo"] as const;

        // Act
        for (const manager of managers) {
          const registry = await createNativeRegistry(
            manager,
            join(h.outputDir, `registry-${manager}`),
          );
          const libName = `${manager}-helper`;
          await h.pm([
            "generate",
            `${registry}:lib-template`,
            "--answer",
            `name=${libName},library=${libName}`,
          ]);
          await h.pm([
            "generate",
            `${registry}:app-template`,
            "--answer",
            `name=${manager}-tool,library=${libName}`,
          ]);
        }
        await h.pm(["install"]);

        // Assert all three native ecosystems run cleanly
        for (const manager of managers) {
          const output = await h.pm(["run", `${manager}-tool:run`]);
          expect(output.stdout.trim()).toBe("hello");
        }
        expect(await h.readFile("Cargo.toml")).toContain("libs/cargo-helper");
        expect(await h.readFile("go.mod")).toContain("module example.com/workspace");
        expect(await pathExists(join(h.workspace, "libs/go-helper/go.mod"))).toBe(false);

        // Act: add a language-neutral template on top
        const genericRegistry = join(h.outputDir, "registry-generic");
        await mkdir(genericRegistry, { recursive: true });
        await writeFile(
          join(genericRegistry, "premise.yaml"),
          `workspace:
  name: generic
  kind: template-registry
  schema-version: "0.2"
  providers:
    ci: github
    tools: mise
    tasks: mise
    infra: pulumi
    versioning: svu
  projects: []
template_registry:
  workspace_files: []
  templates:
    - name: notes
      kind: lib
      path: templates/notes
      questions:
        - prompt: "Name:"
          type: string
          populate: name
`,
          "utf8",
        );
        await copyScaffold(
          join(TESTDATA_ROOT, "package-generation/generic"),
          join(genericRegistry, "templates/notes"),
        );
        const originals = {
          "go.mod": await h.readFile("go.mod"),
          "Cargo.toml": await h.readFile("Cargo.toml"),
          "package.json": await h.readFile("package.json"),
        };

        await h.pm(["generate", `${genericRegistry}:notes`, "--answer", "name=notes"]);

        // Assert native manifests are untouched and generic project is created
        for (const [file, before] of Object.entries(originals)) {
          expect(await h.readFile(file)).toBe(before);
        }
        expect(await h.readFile("libs/notes/README.md")).toContain("generic fixture");
        await h.writeManifest();
      },
    );

    it(
      "when adding Pulumi infra (init --infra or infra setup twice), then infra/ and on-deploy.yml are created while preserving workflows and pins",
      { tags: ["e2e", "cli", "smoke", "workspace", "pulumi", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await scaffoldProject(h, "app", false);
        const onCommitBefore = await h.readFile(".github/workflows/on-commit.yml");

        // Act
        await h.pm(["infra", "setup", "--pulumi-lang", "go"]);

        // Assert infra scaffolded, on-deploy added, existing workflow & app preserved
        await assertScaffoldWorkflows(h.workspace, true);
        expect(await h.readFile("infra/Pulumi.yaml")).toContain("runtime: go");
        expect(await h.readFile(".github/workflows/on-commit.yml")).toBe(onCommitBefore);
        expect(await h.readFile("apps/example/main.go")).toContain('fmt.Println("example")');

        // Act: run infra setup a second time
        const miseBefore = await h.readFile("mise.toml");
        await h.pm(["infra", "setup"]);

        // Assert tool pins remain unchanged
        expect(await h.readFile("mise.toml")).toBe(miseBefore);
        await h.writeManifest();
      },
    );

    it(
      "when generating templates with binary assets and executable modes, then assets, permissions, and provenance are preserved",
      { tags: ["e2e", "cli", "workspace", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        const registry = join(h.outputDir, "registry-binary");
        await mkdir(registry, { recursive: true });
        await h.pm(["init", "--kind", "template-registry"], { cwd: registry });
        await h.pm(["template", "init", "app"], { cwd: registry });
        await chmod(join(registry, "templates/app/mise.toml"), 0o755);
        const binaryBytes = Buffer.from([112, 114, 101, 109, 105, 115, 101, 0, 1]);
        await writeFile(join(registry, "templates/app/asset.bin"), binaryBytes);

        await h.pm(["init"]);

        // Act
        const result = await h.pm(["generate", `${registry}:app`, "--answer", "name=orders"]);

        // Assert
        expect(result.combined).toContain("Generated app orders");
        const generatedMise = await h.readFile("apps/orders/mise.toml");
        expect(generatedMise).toContain("orders: build");
        expect(generatedMise).not.toContain("premise-app");
        const copiedBinary = await readFile(join(h.workspace, "apps/orders/asset.bin"));
        expect(Buffer.compare(copiedBinary, binaryBytes)).toBe(0);
        const mode = (await stat(join(h.workspace, "apps/orders/mise.toml"))).mode & 0o777;
        expect(mode).toBe(0o755);
        expect(await h.readFile("premise.yaml")).toContain("path: apps/orders");
        await h.writeManifest();
      },
    );

    it(
      "when root manifests conflict or candidate contracts fail, then live files, modes, and manifests roll back cleanly",
      { tags: ["e2e", "cli", "workspace", "go", "cargo", "bun", "replay"] },
      async () => {
        // Arrange & Act & Assert for each package manager conflict
        const h = harness();
        const cases: Array<{
          manager: "go" | "cargo" | "bun";
          path: string;
          original: string;
          expectedError: string;
        }> = [
          {
            manager: "go",
            path: "go.mod",
            original: "module example.com/different\ngo 1.25.0\n",
            expectedError: "workspace Go module",
          },
          {
            manager: "cargo",
            path: "Cargo.toml",
            original: "[workspace]\nmembers=[]\nexclude=['libs/*']\nresolver='3'\n",
            expectedError: "excludes generated crate",
          },
          {
            manager: "bun",
            path: "package.json",
            original: '{"name":"client","workspaces":["apps/*","!libs/*"]}',
            expectedError: "excludes generated project",
          },
        ];

        for (const c of cases) {
          const clientDir = join(h.workspace, `conflict-${c.manager}`);
          await mkdir(clientDir, { recursive: true });
          await h.pm(["init"], { cwd: clientDir });
          const registry = await createNativeRegistry(
            c.manager,
            join(h.outputDir, `conflict-registry-${c.manager}`),
          );
          await writeFile(join(clientDir, c.path), c.original, { mode: 0o640 });
          await chmod(join(clientDir, c.path), 0o640);
          const manifestBefore = await readFile(join(clientDir, "premise.yaml"), "utf8");

          const failure = await h.pmFail(
            ["generate", `${registry}:lib-template`, "--answer", "name=helper,library=helper"],
            { cwd: clientDir },
          );

          expect(failure.code).not.toBe(0);
          expect(failure.combined).toContain(c.expectedError);
          expect(await readFile(join(clientDir, c.path), "utf8")).toBe(c.original);
          expect(await readFile(join(clientDir, "premise.yaml"), "utf8")).toBe(manifestBefore);
          expect((await stat(join(clientDir, c.path))).mode & 0o777).toBe(0o640);
          expect(await pathExists(join(clientDir, "libs/helper"))).toBe(false);
          await assertNoTempDirs(join(clientDir, "libs"));
        }
        await h.writeManifest();
      },
    );
  });

  describe("given an existing standalone Cargo crate workspace", () => {
    it(
      "when generating a second crate, then root Cargo.toml includes both and offline tests pass",
      { tags: ["e2e", "cli", "workspace", "cargo", "replay"] },
      async () => {
        // Arrange
        const h = harness();
        await h.pm(["init"]);
        const oldDir = join(h.workspace, "libs/old-helper");
        await copyScaffold(join(TESTDATA_ROOT, "package-generation/cargo/lib"), oldDir);
        const oldCargo = (await readFile(join(oldDir, "Cargo.toml"), "utf8")).replaceAll(
          "lib-template",
          "old-helper",
        );
        await writeFile(join(oldDir, "Cargo.toml"), oldCargo, "utf8");

        const manifest = (await h.readFile("premise.yaml")).replace(
          "    projects: []",
          "    package_managers:\n        - cargo\n    projects:\n        - name: old-helper\n          template: legacy:lib\n          path: libs/old-helper",
        );
        await h.writeFile("premise.yaml", manifest);
        const registry = await createNativeRegistry("cargo", join(h.outputDir, "registry-cargo"));

        // Act
        await h.pm([
          "generate",
          `${registry}:lib-template`,
          "--answer",
          "name=new-helper,library=new-helper",
        ]);
        await h.pm(["install"]);

        // Assert
        const meta = await h.run(
          "cargo",
          ["metadata", "--no-deps", "--format-version=1", "--offline"],
          { expectSuccess: true },
        );
        const parsed = JSON.parse(meta.stdout) as { workspace_members: string[] };
        expect(parsed.workspace_members).toHaveLength(2);
        const rootCargo = await h.readFile("Cargo.toml");
        expect(rootCargo).toContain("libs/old-helper");
        expect(rootCargo).toContain("libs/new-helper");
        await h.run("cargo", ["test", "--offline"], { cwd: oldDir, expectSuccess: true });
        await h.writeManifest();
      },
    );
  });

  describe("given invalid CLI boundaries or manifests", () => {
    it(
      "when invoking pm init, install, generate, or template test on invalid inputs, then each fails with actionable errors",
      { tags: ["e2e", "cli", "smoke", "workspace", "replay"] },
      async () => {
        // Arrange
        const h = harness();

        // Act & Assert: --help names pm
        const help = await h.pm(["--help"]);
        expect(help.stdout).toContain("Usage:\n  pm [command]");

        // Act & Assert: generate in uninitialized dir (outside any parent premise.yaml)
        const uninitDir = await mkdtemp(join(tmpdir(), "premise-uninit-"));
        try {
          const uninitGen = await h.pmFail(["generate", ".:app"], { cwd: uninitDir });
          expect(uninitGen.code).not.toBe(0);
          expect(uninitGen.combined).toContain(
            "Not a premise project. Run pm init to initialize a premise project.",
          );
        } finally {
          await rm(uninitDir, { recursive: true, force: true });
        }

        // Act & Assert: init rejects existing mise.toml without monorepo_root = true
        const badMiseDir = join(h.workspace, "bad-mise");
        await mkdir(badMiseDir, { recursive: true });
        await writeFile(join(badMiseDir, "mise.toml"), '[tools]\nnode = "lts"\n', "utf8");
        const badMiseRes = await h.pmFail(["init"], { cwd: badMiseDir });
        expect(badMiseRes.code).not.toBe(0);
        expect(badMiseRes.combined).toContain("top-level monorepo_root = true");

        // Act & Assert: init preserves existing mise.toml with monorepo_root = true
        const goodMiseDir = join(h.workspace, "good-mise");
        await mkdir(goodMiseDir, { recursive: true });
        const customMise = 'monorepo_root = true\n\n[tools]\nnode = "lts"\n';
        await writeFile(join(goodMiseDir, "mise.toml"), customMise, "utf8");
        await h.pm(["init"], { cwd: goodMiseDir });
        expect(await readFile(join(goodMiseDir, "mise.toml"), "utf8")).toBe(customMise);

        // Act & Assert: init matches embedded workspace-mise.toml and refuses overwrite
        const validDir = join(h.workspace, "valid");
        await mkdir(validDir, { recursive: true });
        await h.pm(["init"], { cwd: validDir });
        const expectedMise = await readFile(
          join(REPO_ROOT, "apps/premise/assets/workspace-mise.toml"),
          "utf8",
        );
        expect(await readFile(join(validDir, "mise.toml"), "utf8")).toBe(expectedMise);
        const reinit = await h.pmFail(["init"], { cwd: validDir });
        expect(reinit.code).not.toBe(0);
        expect(reinit.combined).toContain("already exists");

        // Act & Assert: install from nested directory fails
        const nested = join(validDir, "scratch");
        await mkdir(nested, { recursive: true });
        const nestedInstall = await h.pmFail(["install"], { cwd: nested });
        expect(nestedInstall.code).not.toBe(0);
        expect(nestedInstall.combined).toContain(
          "pm install must run from the premise workspace root",
        );

        // Act & Assert: generate from dir without premise.yaml explains missing manifest
        const notATemplate = join(h.outputDir, "not-a-template");
        await mkdir(notATemplate, { recursive: true });
        const missingManifest = await h.pmFail(["generate", `${notATemplate}:premise-lib`], {
          cwd: validDir,
        });
        expect(missingManifest.code).not.toBe(0);
        expect(missingManifest.combined).toContain(
          `template source "${notATemplate}" does not contain premise.yaml`,
        );

        // Act & Assert: unknown YAML field rejected
        const unknownDir = join(h.workspace, "unknown-yaml");
        await mkdir(unknownDir, { recursive: true });
        await writeFile(
          join(unknownDir, "premise.yaml"),
          `workspace:\n  name: example\n  schema-version: "0.2"\n  projects: []\nunknown: true\n`,
          "utf8",
        );
        const unknownRes = await h.pmFail(["ci", "plan"], { cwd: unknownDir });
        expect(unknownRes.code).not.toBe(0);
        expect(unknownRes.combined).toContain("field unknown not found");

        // Act & Assert: duplicate project names/paths rejected
        const dupDir = join(h.workspace, "dup-projects");
        await mkdir(dupDir, { recursive: true });
        await writeFile(
          join(dupDir, "premise.yaml"),
          `workspace:
  name: example
  schema-version: "0.2"
  providers:
    ci: github
    tools: mise
    tasks: mise
    infra: pulumi
    versioning: svu
  projects:
    - name: orders
      template: .:app
      path: apps/orders
    - name: orders
      template: .:app
      path: apps/orders-v2
`,
          "utf8",
        );
        const dupRes = await h.pmFail(["ci", "plan"], { cwd: dupDir });
        expect(dupRes.code).not.toBe(0);
        expect(dupRes.combined).toContain("duplicate project name");

        // Act & Assert: template registry lifecycle (init, ls, test)
        const regDir = join(h.workspace, "registry");
        await mkdir(regDir, { recursive: true });
        await h.pm(["init", "--kind", "template-registry"], { cwd: regDir });
        await h.pm(["template", "init", "app"], { cwd: regDir });
        await h.pm(["template", "init", "lib"], { cwd: regDir });
        const ls = await h.pm(["template", "ls"], { cwd: regDir });
        expect(ls.stdout).toBe("app\nlib\n");
        await h.pm(["template", "test"], { cwd: regDir });
        await h.writeManifest();
      },
    );
  });
});
