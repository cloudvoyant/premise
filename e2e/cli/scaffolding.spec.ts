/**
 * @module-tag e2e
 * @module-tag cli
 * @module-tag scaffolding
 */
import { chmod, mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { beforeAll, beforeEach, describe, expect, it } from "vitest";
import {
  createNativeRegistry,
  usePremiseHarness,
  useSuiteHarness,
} from "../../libs/testutils/fixtures.js";
import {
  REPO_ROOT,
  TESTDATA_ROOT,
  type PremiseHarness,
} from "../../libs/testutils/premise-harness.js";
import { copyScaffold, pathExists } from "../../libs/tsutils/fs.js";

// Fixtures -------------------------------------------------------------------

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
const GENERIC_REGISTRY = join(TESTDATA_ROOT, "registries/generic");
const SHARED_CARGO_REGISTRY = join(TESTDATA_ROOT, "registries/shared-cargo");
const SHARED_BUN_REGISTRY = join(TESTDATA_ROOT, "registries/shared-bun");

// Tests ----------------------------------------------------------------------

describe("premise scaffolding", { tags: ["e2e", "cli", "scaffolding", "replay"] }, () => {
  const harness = usePremiseHarness();

  describe("Given a fresh directory", () => {
    describe("When scaffolding a lib", { tags: ["smoke", "go"] }, () => {
      const suite = useSuiteHarness("scaffolding_lib", import.meta.filename);
      let libDir = "";
      let planOutput = "";

      beforeAll(async () => {
        const h = suite();
        const res = await scaffoldProject(h, "lib", false);
        libDir = res.projectDir;
        planOutput = (await h.pm(["ci", "plan", "--ci-output"])).stdout;
        await h.writeManifest();
      });

      it("It should scaffold commit and merge workflows without infra or on-deploy", async () => {
        await assertScaffoldWorkflows(suite().workspace, false);
      });

      it("It should substitute template tokens with the project name", async () => {
        expect(await readFile(join(libDir, "go.mod"), "utf8")).toContain(
          "module example.com/example",
        );
        expect(await readFile(join(libDir, "greeting.go"), "utf8")).toContain('return "example"');
        expect(await readFile(join(libDir, "README.md"), "utf8")).toContain(
          "A runnable example project.",
        );
      });

      it("It should create a buildable and testable library with lib-only contracts", async () => {
        await suite().pm(["run", "example:build"]);
        await suite().pm(["run", "example:test"]);
        await assertProjectContracts(libDir, false);
      });

      it("It should not schedule infrastructure deployments", () => {
        expect(planOutput).toContain("has_infra=false\n");
      });
    });

    describe("When scaffolding an app without infra", { tags: ["smoke", "go"] }, () => {
      const suite = useSuiteHarness("scaffolding_app_no_infra", import.meta.filename);
      let appDir = "";

      beforeAll(async () => {
        const h = suite();
        const res = await scaffoldProject(h, "app", false);
        appDir = res.projectDir;
        await h.writeManifest();
      });

      it("It should scaffold commit and merge workflows and omit infra/on-deploy", async () => {
        const h = suite();
        await assertScaffoldWorkflows(h.workspace, false);
        expect(await pathExists(join(h.workspace, "infra"))).toBe(false);
        expect(await pathExists(join(h.workspace, ".github/workflows/on-deploy.yml"))).toBe(false);
      });

      it("It should substitute tokens and create a runnable app with app contracts", async () => {
        expect(await readFile(join(appDir, "go.mod"), "utf8")).toContain(
          "module example.com/example",
        );
        expect(await readFile(join(appDir, "main.go"), "utf8")).toContain('fmt.Println("example")');
        expect((await suite().pm(["run", "example:run"])).stdout.trim()).toBe("example");
        await assertProjectContracts(appDir, true);
      });
    });

    it(
      "It adds Pulumi infra and on-deploy.yml while preserving existing workflows and tool pins",
      { tags: ["smoke", "pulumi"] },
      async () => {
        // Arrange
        const h = harness();
        await scaffoldProject(h, "app", false);
        const onCommitBefore = await h.readFile(".github/workflows/on-commit.yml");

        // Act
        await h.pm(["infra", "setup", "--pulumi-lang", "go"]);

        // Assert
        await assertScaffoldWorkflows(h.workspace, true);
        expect(await h.readFile("infra/env/Pulumi.yaml")).toContain("runtime: go");
        expect(await h.readFile("infra/shared/Pulumi.yaml")).toContain("runtime: go");
        expect(await h.readFile(".github/workflows/on-commit.yml")).toBe(onCommitBefore);
        expect(await h.readFile("apps/example/main.go")).toContain('fmt.Println("example")');

        // Act: run infra setup a second time when infra already exists
        const miseBefore = await h.readFile("mise.toml");
        const repeated = await h.pmFail(["infra", "setup", "--pulumi-lang", "go"]);

        // Assert repeated setup is rejected and tool pins remain unchanged
        expect(repeated.code).not.toBe(0);
        expect(repeated.combined).toContain("already exists");
        expect(await h.readFile("mise.toml")).toBe(miseBefore);
        await h.writeManifest();
      },
    );
  });

  describe("Given an initialized monorepo", () => {
    beforeEach(async () => {
      await harness().pm(["init"]);
    });

    for (const manager of ["go", "cargo", "bun"] as const) {
      describe(`When generating a ${manager} lib+app pair`, { tags: [manager] }, () => {
        const suite = useSuiteHarness(`scaffolding_native_${manager}`, import.meta.filename);
        const lockFile = manager === "cargo" ? "Cargo.lock" : manager === "bun" ? "bun.lock" : "";
        let registry = "";
        let initialRules = "";
        let clientRules = "";
        let lockExistsAfterGen = false;

        beforeAll(async () => {
          const h = suite();
          await h.pm(["init"]);
          registry = await createNativeRegistry(manager, join(h.outputDir, `registry-${manager}`));
          await h.pm([
            "generate",
            `${registry}:lib-template`,
            "--answer",
            "name=helper,library=helper",
          ]);
          initialRules = await h.readFile(".editorconfig");
          clientRules = `${initialRules}\n# Client quality rules\n`;
          await h.writeFile(".editorconfig", clientRules, 0o644);

          if (lockFile) {
            await h.writeFile(lockFile, "stale lock fixture", 0o640);
            await writeFile(
              join(registry, "templates/app-template", lockFile),
              "stale template lock fixture",
              "utf8",
            );
          }

          await h.pm([
            "generate",
            `${registry}:app-template`,
            "--answer",
            "name=tool,library=helper",
          ]);
          lockExistsAfterGen = lockFile ? await pathExists(join(h.workspace, lockFile)) : true;
          await h.pm(["install"]);
          await h.writeManifest();
        });

        it("It should initialize quality rules once and preserve client edits", async () => {
          const h = suite();
          expect(initialRules).toBe(await readFile(join(registry, ".editorconfig"), "utf8"));
          expect(await h.readFile(".editorconfig")).toBe(clientRules);
          expect(await pathExists(join(h.workspace, "libs/helper/.editorconfig"))).toBe(false);
          expect(await pathExists(join(h.workspace, "apps/tool/.editorconfig"))).toBe(false);
        });

        it("It should regenerate stale root lockfiles while preserving permissions", async () => {
          const h = suite();
          expect(lockExistsAfterGen).toBe(true);
          if (lockFile) {
            expect(await h.readFile(lockFile)).not.toContain("stale");
            expect(await pathExists(join(h.workspace, "apps/tool", lockFile))).toBe(false);
            expect((await stat(join(h.workspace, lockFile))).mode & 0o777).toBe(0o640);
          }
        });

        it(`It should build, test, and run the ${manager} workspace natively`, async () => {
          const h = suite();
          expect((await h.pm(["run", "tool:run"])).stdout.trim()).toBe("hello");
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
            expect(
              (JSON.parse(meta.stdout) as { workspace_members: string[] }).workspace_members,
            ).toHaveLength(2);
            await h.run("cargo", ["fetch", "--offline"], { expectSuccess: true });
            expect(await pathExists(join(h.workspace, "Cargo.lock"))).toBe(true);
          } else {
            expect(await pathExists(join(h.workspace, "bun.lock"))).toBe(true);
            await h.run("bun", ["install", "--ignore-scripts", "--frozen-lockfile"], {
              cwd: join(h.workspace, "apps/tool"),
              expectSuccess: true,
            });
            const rootPkg = await h.readFile("package.json");
            expect(rootPkg).not.toContain("registry-development");
            expect(rootPkg).not.toContain("templates/");
          }
        });

        it("It should record project provenance and leave no temporary directories", async () => {
          const h = suite();
          const manifest = await h.readFile("premise.yaml");
          expect(manifest).toContain("name: helper");
          expect(manifest).toContain("name: tool");
          await assertNoTempDirs(join(h.workspace, "apps"), join(h.workspace, "libs"));
        });
      });
    }

    for (const order of [
      ["bun", "cargo"],
      ["cargo", "bun"],
    ] as const) {
      it(
        `It combines Go with ${order.join(" then ")} and a language-neutral template cleanly`,
        { tags: ["go", "cargo", "bun"] },
        async () => {
          // Arrange
          const h = harness();
          const managers = ["go", ...order] as const;

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
            expect((await h.pm(["run", `${manager}-tool:run`])).stdout.trim()).toBe("hello");
          }
          expect(await h.readFile("Cargo.toml")).toContain("libs/cargo-helper");
          expect(await h.readFile("go.mod")).toContain("module example.com/workspace");
          expect(await pathExists(join(h.workspace, "libs/go-helper/go.mod"))).toBe(false);

          // Act: add a language-neutral template from e2e/testdata/registries/generic
          const originals = {
            "go.mod": await h.readFile("go.mod"),
            "Cargo.toml": await h.readFile("Cargo.toml"),
            "package.json": await h.readFile("package.json"),
          };
          await h.pm(["generate", `${GENERIC_REGISTRY}:notes`, "--answer", "name=notes"]);

          // Assert
          for (const [file, before] of Object.entries(originals)) {
            expect(await h.readFile(file)).toBe(before);
          }
          expect(await h.readFile("libs/notes/README.md")).toContain("generic fixture");
          await h.writeManifest();
        },
      );
    }

    it(
      "It merges shared root .gitignore, .gitattributes, and mise.toml across Cargo and Bun registries",
      { tags: ["cargo", "bun"] },
      async () => {
        // Arrange
        const h = harness();

        // Act: generate directly from committed testdata registries
        await h.pm([
          "generate",
          `${SHARED_CARGO_REGISTRY}:rust-cli`,
          "--answer",
          "name=orders-cli",
        ]);
        await h.pm(["generate", `${SHARED_BUN_REGISTRY}:hono-api`, "--answer", "name=gateway-api"]);

        // Assert
        const rootGitignore = await h.readFile(".gitignore");
        expect(rootGitignore).toContain("/target");
        expect(rootGitignore).toContain("node_modules/");
        const rootAttrs = await h.readFile(".gitattributes");
        expect(rootAttrs).toContain("*.rs text eol=lf");
        expect(rootAttrs).toContain("*.ts text eol=lf");
        const rootMise = await h.readFile("mise.toml");
        expect(rootMise).toContain("CARGO_SHARED_ROOT");
        expect(rootMise).toContain("BUN_SHARED_ROOT");
        expect(await h.readFile("package.json")).not.toContain("gateway-api");
        expect(await h.readFile("apps/orders-cli/Cargo.toml")).toContain('name = "orders-cli"');
        expect(await h.readFile("apps/gateway-api/package.json")).toContain("gateway-api");
        expect(await h.readFile("Cargo.toml")).toContain("apps/orders-cli");
        await assertNoTempDirs(join(h.workspace, "apps"));
        await h.writeManifest();
      },
    );

    it("It preserves binary assets, executable file modes, and project provenance", async () => {
      // Arrange
      const h = harness();
      const registry = join(h.outputDir, "registry-binary");
      await mkdir(registry, { recursive: true });
      await h.pm(["init", "--kind", "template-registry"], { cwd: registry });
      await h.pm(["template", "init", "app"], { cwd: registry });
      await chmod(join(registry, "templates/app/mise.toml"), 0o755);
      const binaryBytes = Buffer.from([112, 114, 101, 109, 105, 115, 101, 0, 1]);
      await writeFile(join(registry, "templates/app/asset.bin"), binaryBytes);

      // Act
      const result = await h.pm(["generate", `${registry}:app`, "--answer", "name=orders"]);

      // Assert
      expect(result.combined).toContain("Generated app orders");
      const generatedMise = await h.readFile("apps/orders/mise.toml");
      expect(generatedMise).toContain("orders: build");
      expect(generatedMise).not.toContain("premise-app");
      const copiedBinary = await readFile(join(h.workspace, "apps/orders/asset.bin"));
      expect(Buffer.compare(copiedBinary, binaryBytes)).toBe(0);
      expect((await stat(join(h.workspace, "apps/orders/mise.toml"))).mode & 0o777).toBe(0o755);
      expect(await h.readFile("premise.yaml")).toContain("path: apps/orders");
      await h.writeManifest();
    });

    it(
      "It rolls back files, modes, and manifests when root manifests conflict",
      { tags: ["go", "cargo", "bun"] },
      async () => {
        // Arrange
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

        // Act & Assert
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

    it(
      "It rolls back live state, locks, modes, and temp dirs when selective tool updates or candidate contracts fail",
      { tags: ["cargo", "bun"] },
      async () => {
        // Arrange
        const h = harness();
        const shimBin = await installMiseFailureShim(h);
        const cases = [
          {
            name: "selective-tool-update",
            registry: SHARED_BUN_REGISTRY,
            template: "hono-api",
            project: "broken-api",
            nativePath: "package.json",
            nativeOriginal: '{"name":"client","private":true,"workspaces":["apps/*"]}',
            extraRootMise: "\n[tools]\nbun = '1.1'\n",
            failEnv: { MISE_FAIL_MISE_CONTAINS: "bun = '1.2'" },
            wantError: "tool update bun 1.1 -> 1.2 failed",
          },
          {
            name: "final-candidate-contract",
            registry: SHARED_CARGO_REGISTRY,
            template: "rust-cli",
            project: "broken-cli",
            nativePath: "Cargo.toml",
            nativeOriginal: "[workspace]\nmembers=[]\nresolver='2'\n",
            extraRootMise: "",
            failEnv: {
              MISE_FAIL_DIRECTORY: "premise-candidate-validation-",
              MISE_FAIL_ARGUMENT: "run e2e",
            },
            wantError: "generated candidate validation failed",
          },
        ];

        // Act & Assert
        for (const c of cases) {
          const clientDir = join(h.workspace, c.name);
          const tempDir = join(h.outputDir, `tmp-${c.name}`);
          await mkdir(clientDir, { recursive: true });
          await mkdir(tempDir, { recursive: true });
          await h.pm(["init"], { cwd: clientDir });
          if (c.extraRootMise) {
            const miseBefore = await readFile(join(clientDir, "mise.toml"), "utf8");
            await writeFile(join(clientDir, "mise.toml"), miseBefore + c.extraRootMise, "utf8");
          }
          await writeFile(join(clientDir, c.nativePath), c.nativeOriginal, { mode: 0o640 });
          await chmod(join(clientDir, c.nativePath), 0o640);
          const manifestBefore = await readFile(join(clientDir, "premise.yaml"), "utf8");

          const failure = await h.pmFail(
            ["generate", `${c.registry}:${c.template}`, "--answer", `name=${c.project}`],
            {
              cwd: clientDir,
              env: {
                PATH: `${shimBin}:${process.env.PATH ?? ""}`,
                TMPDIR: tempDir,
                ...c.failEnv,
              },
            },
          );

          expect(failure.code).not.toBe(0);
          expect(failure.combined).toContain(c.wantError);
          expect(await pathExists(join(clientDir, "apps", c.project))).toBe(false);
          expect(await readFile(join(clientDir, c.nativePath), "utf8")).toBe(c.nativeOriginal);
          expect((await stat(join(clientDir, c.nativePath))).mode & 0o777).toBe(0o640);
          expect(await pathExists(join(clientDir, "bun.lock"))).toBe(false);
          expect(await pathExists(join(clientDir, "Cargo.lock"))).toBe(false);
          expect(await readFile(join(clientDir, "premise.yaml"), "utf8")).toBe(manifestBefore);
          await assertNoTempDirs(join(clientDir, "apps"), tempDir);
        }
        await h.writeManifest();
      },
    );
  });

  describe("Given an existing standalone Cargo crate workspace", () => {
    beforeEach(async () => {
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
    });

    it(
      "It includes both existing and generated crates in root Cargo.toml and passes offline tests",
      { tags: ["cargo"] },
      async () => {
        // Arrange
        const h = harness();
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
        expect(
          (JSON.parse(meta.stdout) as { workspace_members: string[] }).workspace_members,
        ).toHaveLength(2);
        const rootCargo = await h.readFile("Cargo.toml");
        expect(rootCargo).toContain("libs/old-helper");
        expect(rootCargo).toContain("libs/new-helper");
        await h.run("cargo", ["test", "--offline"], {
          cwd: join(h.workspace, "libs/old-helper"),
          expectSuccess: true,
        });
        await h.writeManifest();
      },
    );
  });

  describe("Given invalid CLI boundaries or manifests", () => {
    it(
      "It fails with actionable errors on invalid init, install, generate, manifest, and template contract inputs",
      { tags: ["smoke"] },
      async () => {
        // Arrange
        const h = harness();

        // Act & Assert: --help names pm
        expect((await h.pm(["--help"])).stdout).toContain("Usage:\n  pm [command]");

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
        expect((await h.pmFail(["init"], { cwd: badMiseDir })).combined).toContain(
          "top-level monorepo_root = true",
        );

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
        expect(await readFile(join(validDir, "mise.toml"), "utf8")).toBe(
          await readFile(join(REPO_ROOT, "apps/premise/assets/workspace-mise.toml"), "utf8"),
        );
        expect((await h.pmFail(["init"], { cwd: validDir })).combined).toContain("already exists");

        // Act & Assert: install from nested directory fails
        const nested = join(validDir, "scratch");
        await mkdir(nested, { recursive: true });
        expect((await h.pmFail(["install"], { cwd: nested })).combined).toContain(
          "pm install must run from the premise workspace root",
        );

        // Act & Assert: generate from dir without premise.yaml explains missing manifest
        const notATemplate = join(h.outputDir, "not-a-template");
        await mkdir(notATemplate, { recursive: true });
        expect(
          (await h.pmFail(["generate", `${notATemplate}:premise-lib`], { cwd: validDir })).combined,
        ).toContain(`template source "${notATemplate}" does not contain premise.yaml`);

        // Act & Assert: unknown YAML field, duplicate project name, and duplicate project path rejected
        const unknownDir = join(h.workspace, "unknown-yaml");
        await mkdir(unknownDir, { recursive: true });
        await writeFile(
          join(unknownDir, "premise.yaml"),
          'workspace:\n  name: example\n  schema-version: "0.2"\n  projects: []\nunknown: true\n',
          "utf8",
        );
        expect((await h.pmFail(["ci", "plan"], { cwd: unknownDir })).combined).toContain(
          "field unknown not found",
        );

        const dupNameDir = join(h.workspace, "dup-project-name");
        await mkdir(dupNameDir, { recursive: true });
        await writeFile(
          join(dupNameDir, "premise.yaml"),
          `workspace:
  name: example
  schema-version: "0.2"
  providers: {ci: github, tools: mise, tasks: mise, infra: pulumi, versioning: svu}
  projects:
    - {name: orders, template: ".:app", path: apps/orders}
    - {name: orders, template: ".:app", path: apps/orders-v2}
`,
          "utf8",
        );
        expect((await h.pmFail(["ci", "plan"], { cwd: dupNameDir })).combined).toContain(
          "duplicate project name",
        );

        const dupPathDir = join(h.workspace, "dup-project-path");
        await mkdir(dupPathDir, { recursive: true });
        await writeFile(
          join(dupPathDir, "premise.yaml"),
          `workspace:
  name: example
  schema-version: "0.2"
  providers: {ci: github, tools: mise, tasks: mise, infra: pulumi, versioning: svu}
  projects:
    - {name: orders, template: ".:app", path: apps/orders}
    - {name: payments, template: ".:app", path: apps/orders}
`,
          "utf8",
        );
        expect((await h.pmFail(["ci", "plan"], { cwd: dupPathDir })).combined).toContain(
          "duplicate project path",
        );

        // Act & Assert: template registry lifecycle & multi-failure contract aggregation
        const regDir = join(h.workspace, "registry");
        await mkdir(regDir, { recursive: true });
        await h.pm(["init", "--kind", "template-registry"], { cwd: regDir });
        await h.pm(["template", "init", "app"], { cwd: regDir });
        await h.pm(["template", "init", "lib"], { cwd: regDir });
        expect((await h.pm(["template", "ls"], { cwd: regDir })).stdout).toBe("app\nlib\n");
        await h.pm(["template", "test"], { cwd: regDir });

        const failBin = join(h.outputDir, "fail-mise-bin");
        await mkdir(failBin, { recursive: true });
        await writeFile(join(failBin, "mise"), "#!/bin/sh\nexit 1\n", { mode: 0o755 });
        await chmod(join(failBin, "mise"), 0o755);
        const contractFail = await h.pmFail(["template", "test"], {
          cwd: regDir,
          env: { PATH: `${failBin}:${process.env.PATH ?? ""}` },
        });
        expect(contractFail.code).not.toBe(0);
        for (const expected of [
          "template app task build failed",
          "template app task e2e failed",
          "template lib task publish:rc failed",
          "template lib task publish failed",
        ]) {
          expect(contractFail.combined).toContain(expected);
        }
        await h.writeManifest();
      },
    );
  });
});

// Private helpers ------------------------------------------------------------

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
    expect(await readFile(join(workspace, ".github/workflows", name), "utf8")).toContain(
      "cloudvoyant/premise@v0\n",
    );
  }
  expect(await readFile(join(workspace, ".github/workflows/on-commit.yml"), "utf8")).toContain(
    "if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name != github.repository\n",
  );

  const deployPath = join(workspace, ".github/workflows/on-deploy.yml");
  if (infrastructure) {
    expect(await readFile(deployPath, "utf8")).toContain("workflow_dispatch:");
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
    expect(new RegExp(`\\[tasks\\.("?${task}"?)\\]`).test(content)).toBe(isApp);
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

async function installMiseFailureShim(h: PremiseHarness): Promise<string> {
  const shimBin = join(h.outputDir, "mise-failure-shim");
  await mkdir(shimBin, { recursive: true });
  const script = `#!/bin/sh
if [ -n "\${MISE_FAIL_DIRECTORY:-}" ]; then
  case "$PWD" in
    *"$MISE_FAIL_DIRECTORY"*)
      if [ -z "\${MISE_FAIL_ARGUMENT:-}" ] || [ "$*" = "$MISE_FAIL_ARGUMENT" ]; then exit 7; fi
      ;;
  esac
fi
if [ -n "\${MISE_FAIL_MISE_CONTAINS:-}" ] && [ -f mise.toml ] && grep -F "$MISE_FAIL_MISE_CONTAINS" mise.toml >/dev/null; then
  exit 8
fi
exit 0
`;
  await writeFile(join(shimBin, "mise"), script, { mode: 0o755 });
  await chmod(join(shimBin, "mise"), 0o755);
  return shimBin;
}
