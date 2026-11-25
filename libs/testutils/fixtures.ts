import { cp, mkdir, readdir, readFile, stat, writeFile } from "node:fs/promises";
import { join, relative } from "node:path";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";
import { hostCIPlatform, sha256 } from "../tsutils/fs.js";
import {
  assertGcsBucketDeleted,
  destroyPulumiStack,
  patchManifestInfra,
  resolveLiveGcpConfig,
} from "../tsutils/gcsx.js";
import type { E2EMode } from "./mode.js";
import { PremiseHarness, TESTDATA_ROOT, type TestTaskMeta } from "./premise-harness.js";
import { installPmShim } from "./shims.js";

// Public API -----------------------------------------------------------------

/** Copy a testdata scaffold directory while excluding VCS, dependency, and Pulumi stack state. */
export async function copyScaffold(source: string, destination: string): Promise<void> {
  await mkdir(destination, { recursive: true });
  await cp(source, destination, {
    recursive: true,
    filter: (src) => {
      const name = src.split("/").at(-1) ?? "";
      if (name === ".pulumi" || name === "node_modules" || name === ".git") return false;
      if (name.startsWith("Pulumi.") && name !== "Pulumi.yaml") return false;
      return true;
    },
  });
}

/** Collect deterministic path, SHA-256, and byte-size entries for a test workspace manifest. */
export async function collectLocalFiles(
  root: string,
  base = root,
): Promise<Array<{ path: string; sha256: string; bytes: number }>> {
  const results: Array<{ path: string; sha256: string; bytes: number }> = [];
  let entries;
  try {
    entries = await readdir(root, { withFileTypes: true });
  } catch {
    return results;
  }
  for (const entry of entries) {
    if (
      entry.name === ".git" ||
      entry.name === "node_modules" ||
      entry.name === "target" ||
      entry.name === ".pulumi"
    )
      continue;
    const full = join(root, entry.name);
    if (entry.isDirectory()) {
      results.push(...(await collectLocalFiles(full, base)));
    } else if (entry.isFile()) {
      const data = await readFile(full);
      const info = await stat(full);
      results.push({
        path: relative(base, full).replaceAll("\\", "/"),
        sha256: sha256(data),
        bytes: info.size,
      });
    }
  }
  results.sort((a, b) => a.path.localeCompare(b.path));
  return results;
}

export interface LiveInfraContext {
  branch: string;
  primaryBackend: string;
  alternateBackend: string;
  gcpProject: string;
  touched: Set<string>;
  buckets: Map<string, string>;
}

/** Own an isolated PremiseHarness per `it(...)` test case. */
export function usePremiseHarness(
  options: {
    fixture?: string;
    mode?: E2EMode;
    env?: Record<string, string | undefined>;
  } = {},
) {
  let current: PremiseHarness | undefined;

  beforeEach(async (ctx) => {
    const task: TestTaskMeta = {
      filepath: ctx.task.file?.filepath ?? "",
      name: ctx.task.name,
      fullName: ctx.task.suite ? `${ctx.task.suite.name} > ${ctx.task.name}` : ctx.task.name,
    };
    current = await PremiseHarness.create({
      task,
      fixture: options.fixture,
      mode: options.mode,
      env: options.env,
    });
  });

  afterEach(async () => {
    if (current) {
      await current.dispose();
      current = undefined;
    }
  });

  return () => {
    if (!current) throw new Error("PremiseHarness accessed outside active test");
    return current;
  };
}

/** Own a shared PremiseHarness for a `describe('When ...')` block using `beforeAll`/`afterAll`. */
export function useSuiteHarness(
  suiteName: string,
  specFile: string,
  options: {
    fixture?: string;
    mode?: E2EMode;
    env?: Record<string, string | undefined>;
    enabled?: boolean;
  } = {},
) {
  let current: PremiseHarness | undefined;

  beforeAll(async () => {
    if (options.enabled === false) return;
    current = await PremiseHarness.create({
      task: { filepath: specFile, name: suiteName, fullName: suiteName },
      fixture: options.fixture,
      mode: options.mode,
      env: options.env,
    });
  });

  afterAll(async () => {
    if (current) {
      await current.dispose();
      current = undefined;
    }
  });

  return () => {
    if (!current)
      throw new Error(`Suite PremiseHarness "${suiteName}" accessed outside active suite`);
    return current;
  };
}

export async function setupGenericInfraWorkspace(h: PremiseHarness): Promise<void> {
  await copyScaffold(join(TESTDATA_ROOT, "pulumi/client"), h.workspace);
  await h.writeFile(
    "mise.toml",
    "monorepo_root = true\n[monorepo]\nconfig_roots = ['apps/*', 'libs/*']\n",
  );
  const manifestPath = join(h.workspace, "premise.yaml");
  const manifest = await readFile(manifestPath, "utf8");
  const platform = hostCIPlatform();
  const updated = manifest.includes("release_platforms:")
    ? manifest.replace(/release_platforms:.*$/m, `release_platforms: [${platform}]`)
    : manifest.replace(
        "path: apps/web",
        `path: apps/web\n      ci:\n        release_platforms:\n          - ${platform}`,
      );
  await writeFile(manifestPath, updated, "utf8");
  await h.initGit("chore: initial project", "v1.0.0");
}

export async function commitChange(h: PremiseHarness, message: string): Promise<void> {
  await h.writeFile("CHANGE.txt", `${message}\n`);
  await h.git(["add", "CHANGE.txt"]);
  await h.git(["commit", "--quiet", "-m", message]);
}

export async function configureLiveInfraWorkspace(
  h: PremiseHarness,
  options: { copyFixture?: boolean; initGit?: boolean } = {},
): Promise<LiveInfraContext> {
  if (options.copyFixture) {
    await copyScaffold(join(TESTDATA_ROOT, "pulumi/client"), h.workspace);
  }
  const { project: gcpProject, backendUrl: baseBackend } = resolveLiveGcpConfig();
  const runBackend = `${baseBackend.replace(/\/+$/, "")}/run-${h.runId}`;
  const primaryBackend = runBackend;
  const alternateBackend = `${runBackend}/alternate`;
  const branch = `feature/static-${h.runId}`;

  const shimBin = await installPmShim(h);
  h.env.PATH = `${shimBin}:${h.env.PATH ?? process.env.PATH ?? ""}`;
  h.env.PULUMI_CONFIG_PASSPHRASE = "";
  h.env.PM_RELEASE = "A";
  h.env.PM_BRANCH = "";
  h.env.PULUMI_BACKEND_URL = `${runBackend}/unselected-fallback`;

  const manifestPath = join(h.workspace, "premise.yaml");
  const original = await readFile(manifestPath, "utf8");
  await writeFile(
    manifestPath,
    patchManifestInfra(original, { primaryBackend, alternateBackend, gcpProject }),
    "utf8",
  );
  if (options.initGit) {
    await h.initGit("chore: initial project", "v1.0.0");
  }

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
          infraDir: join(h.workspace, env === "shared" ? "infra/shared" : "infra/env"),
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

/** Build a local native template registry (Go, Cargo, or Bun) from e2e/testdata/package-generation/. */
export async function createNativeRegistry(
  manager: "go" | "cargo" | "bun",
  destination: string,
): Promise<string> {
  await mkdir(destination, { recursive: true });
  const sharedEditorconfig = await readFile(
    join(TESTDATA_ROOT, "package-generation/shared/.editorconfig"),
    "utf8",
  );
  await writeFile(join(destination, ".editorconfig"), sharedEditorconfig, "utf8");

  const workspaceFiles: string[] = [];
  if (manager === "cargo") {
    workspaceFiles.push("Cargo.toml");
    await writeFile(
      join(destination, "Cargo.toml"),
      await readFile(join(TESTDATA_ROOT, "package-generation/cargo/root.Cargo.toml"), "utf8"),
      "utf8",
    );
  }
  if (manager === "bun") {
    workspaceFiles.push("package.json", "mise.toml");
    await writeFile(
      join(destination, "mise.toml"),
      await readFile(join(TESTDATA_ROOT, "package-generation/bun/root.mise.toml"), "utf8"),
      "utf8",
    );
    await writeFile(
      join(destination, "package.json"),
      JSON.stringify({
        name: "registry-development",
        private: true,
        workspaces: ["templates/*", "apps/*", "libs/*"],
      }),
      "utf8",
    );
  }

  for (const kind of ["lib", "app"] as const) {
    await copyScaffold(
      join(TESTDATA_ROOT, "package-generation", manager, kind),
      join(destination, "templates", `${kind}-template`),
    );
  }

  const manifest = `workspace:
  name: registry-development
  kind: template-registry
  schema-version: "0.2"
  package_managers:
    - ${manager}
  providers:
    ci: github
    tools: mise
    tasks: mise
    infra: pulumi
    versioning: svu
  projects: []
template_registry:
  workspace_files: ${JSON.stringify(workspaceFiles)}
  workspace_init_files:
    - .editorconfig
  templates:
    - name: lib-template
      kind: lib
      path: templates/lib-template
      version: 0.1.0
      questions:
        - prompt: "Name:"
          type: string
          populate: name
        - prompt: "Sibling name:"
          type: string
          populate: library
      substitutions:
        lib-template: name
        LIBRARY_NAME: library
    - name: app-template
      kind: app
      path: templates/app-template
      version: 0.1.0
      questions:
        - prompt: "Name:"
          type: string
          populate: name
        - prompt: "Sibling name:"
          type: string
          populate: library
      substitutions:
        app-template: name
        LIBRARY_NAME: library
`;
  await writeFile(join(destination, "premise.yaml"), manifest, "utf8");
  return destination;
}

/** Configure a Cargo workspace with only private-registry crates (public-lib depending on private-lib) and no native releases. */
export async function setupPrivateCargoWorkspace(h: PremiseHarness): Promise<void> {
  await copyScaffold(join(TESTDATA_ROOT, "workspaces/cargo"), h.workspace);
  await h.writeFile(
    "premise.yaml",
    `workspace:
  name: cargo-private-only
  kind: monorepo
  schema-version: "0.2"
  package_managers:
    - cargo
  providers:
    ci: github
    tools: mise
    tasks: mise
    infra: pulumi
    versioning: svu
  projects:
    - name: private-lib
      template: cloudvoyant/premise-cargo:rust-lib
      version: 0.1.0
      path: libs/private-lib
    - name: public-lib
      template: cloudvoyant/premise-cargo:rust-lib
      version: 0.1.0
      path: libs/public-lib
`,
  );
  await h.writeFile(
    "libs/public-lib/Cargo.toml",
    `[package]
name = "public-lib"
version = "0.1.0"
edition = "2021"
publish = ["premise-private"]

[dependencies]
private-lib = { path = "../private-lib" }
`,
  );
  await h.writeFile(
    "Cargo.toml",
    `[workspace]
members = ["libs/private-lib", "libs/public-lib"]
resolver = "2"
`,
  );
}
