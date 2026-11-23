import { afterEach, beforeEach } from "vitest";
import { join } from "node:path";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { PremiseHarness, TESTDATA_ROOT, type TestTaskMeta } from "./premise-harness.js";
import { copyScaffold, type E2EMode } from "./test-utils.js";

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

/** Build a realistic local native template registry (Go, Cargo, or Bun) for scaffold tests. */
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
    const rootCargo = await readFile(
      join(TESTDATA_ROOT, "package-generation/cargo/root.Cargo.toml"),
      "utf8",
    );
    await writeFile(join(destination, "Cargo.toml"), rootCargo, "utf8");
  }
  if (manager === "bun") {
    workspaceFiles.push("package.json", "mise.toml");
    const rootMise = await readFile(
      join(TESTDATA_ROOT, "package-generation/bun/root.mise.toml"),
      "utf8",
    );
    await writeFile(join(destination, "mise.toml"), rootMise, "utf8");
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
