import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import * as fs from "node:fs";
import * as path from "node:path";
import YAML from "yaml";

const here = path.dirname(fileURLToPath(import.meta.url));
export const workspace = path.resolve(process.argv[2] ?? "test-results/workspace");
export const pm = process.env.PREMISE_BIN ?? "pm";

export function command(name, args, directory, extraEnv = {}, administration = false) {
  const environment = { ...process.env, ...extraEnv };
  if (!administration)
    for (const key of [
      "NEON_API_KEY",
      "VERCEL_TOKEN",
      "VERCEL_API_TOKEN",
      "PULUMI_ACCESS_TOKEN",
      "GITHUB_TOKEN",
      "GH_TOKEN",
      "NPM_TOKEN",
      "NODE_AUTH_TOKEN",
      "PULUMI_CONFIG_PASSPHRASE",
    ])
      delete environment[key];
  const result = spawnSync(name, args, {
    cwd: directory,
    env: environment,
    stdio: "inherit",
  });
  if (result.status !== 0) throw new Error(`${name} failed with status ${result.status}`);
}

export function scaffold() {
  if (fs.existsSync(path.join(workspace, "premise.yaml")))
    throw new Error("Use a fresh example workspace");
  fs.mkdirSync(workspace, { recursive: true });
  command(pm, ["init"], workspace);
  const bunRegistry = process.env.PREMISE_BUN_REGISTRY ?? "cloudvoyant/premise-bun";
  const pulumiRegistry = process.env.PREMISE_PULUMI_REGISTRY ?? process.env.PREMISE_PULUMI_REGISTRY;
  command(
    pm,
    ["g", `${bunRegistry}:premise-tanstack-start-app`, "--answer", "name=web"],
    workspace,
  );
  command(pm, ["g", `${pulumiRegistry}:environment`, "--answer", "name=environment"], workspace);
  command("mise", ["trust", path.join(workspace, "mise.toml")], workspace);
  command("mise", ["trust", path.join(workspace, "infra/environment/mise.toml")], workspace);
  for (const [template, name] of [
    ["neon-db", "database"],
    ["vercel-app", "web-deployment"],
    ["vercel-app", "admin-deployment"],
  ]) {
    command(pm, ["g", `${pulumiRegistry}:${template}`, "--answer", `name=${name}`], workspace);
    const directory = path.join(workspace, "infra/components", name);
    command("mise", ["trust", path.join(directory, "mise.toml")], workspace);
    command("mise", ["run", "install"], directory);
  }
  const localComponent = path.join(workspace, "infra/components/client");
  fs.cpSync(path.join(here, "client-component"), localComponent, { recursive: true });
  command("mise", ["trust", path.join(localComponent, "mise.toml")], workspace);
  command("mise", ["run", "install"], localComponent);
  command("mise", ["run", "test"], localComponent);
  const program = path.join(workspace, "infra/environment");
  fs.cpSync(path.join(here, "program"), program, { recursive: true });
  command("go", ["mod", "tidy"], program);
  const app = path.join(workspace, "apps/web");
  fs.cpSync(path.join(here, "app-overlay"), app, { recursive: true });
  const packageFile = path.join(app, "package.json");
  const pkg = JSON.parse(fs.readFileSync(packageFile));
  Object.assign(pkg.dependencies, {
    "@tanstack/react-start": "1.168.60",
    "@tanstack/react-router": "1.170.41",
    "@neondatabase/serverless": "1.2.0",
    "drizzle-orm": "0.45.4",
    pg: "8.23.1",
    nitro: "3.0.260903-beta",
  });
  Object.assign(pkg.devDependencies, { "@types/pg": "8.15.6" });
  fs.writeFileSync(packageFile, `${JSON.stringify(pkg, null, 2)}\n`);
  const viteFile = path.join(app, "vite.config.ts");
  const vite = fs
    .readFileSync(viteFile, "utf8")
    .replace("import react from", "import { nitro } from 'nitro/vite'\nimport react from")
    .replace("tanstackStart(), react()", "tanstackStart(), nitro(), react()");
  fs.writeFileSync(viteFile, vite);
  fs.writeFileSync(path.join(app, "vercel.json"), '{"framework":"tanstack-start"}\n');
  fs.appendFileSync(
    path.join(app, "mise.toml"),
    `
[tasks.migrate]
run = "bun scripts/migrate.ts"

[tasks.health]
run = "node scripts/health.mjs"

[tasks.rollback]
run = "node scripts/rollback.mjs"

[tasks.cleanup]
run = "node scripts/cleanup.mjs"
`,
  );
  const manifestFile = path.join(workspace, "premise.yaml");
  const manifest = YAML.parse(fs.readFileSync(manifestFile, "utf8"));
  manifest.workspace.projects.push({
    name: "client-identity",
    path: "infra/components/client",
    infra: { role: "component" },
  });
  const required = (key) => {
    if (!process.env[key]) throw new Error(`${key} is required`);
    return process.env[key];
  };
  const config = {
    gcpProject: required("GCP_PROJECT_ID"),
    neonProjectId: required("NEON_PROJECT_ID"),
    neonParentBranchId: required("NEON_PARENT_BRANCH_ID"),
    vercelProjectId: required("VERCEL_PROJECT_ID"),
    vercelTeamId: required("VERCEL_TEAM_ID"),
    vercelTeamSlug: process.env.VERCEL_TEAM_SLUG ?? "cloudvoyant",
  };
  const web = manifest.workspace.projects.find((project) => project.name === "web");
  web.secrets = {
    config: "fnox.toml",
    profiles: Object.fromEntries(["dev", "stage", "prod", "preview"].map((env) => [env, env])),
    keys: ["APP_MARKER"],
    file: ".env.local",
  };
  fs.writeFileSync(
    path.join(app, "fnox.toml"),
    `[providers.gcp]
type = "gcp-sm"
project = "${required("GCP_PROJECT_ID")}"

[profiles.dev.secrets.APP_MARKER]
provider = "gcp"
value = "premise-e2e-app-marker"

[profiles.stage.secrets.APP_MARKER]
provider = "gcp"
value = "premise-stage-app-marker"

[profiles.prod.secrets.APP_MARKER]
provider = "gcp"
value = "premise-prod-app-marker"

[profiles.preview.secrets.APP_MARKER]
provider = "gcp"
value = "premise-e2e-app-marker"
`,
  );
  const programProject = manifest.workspace.projects.find(
    (project) => project.name === "environment",
  );
  programProject.secrets = { ...web.secrets };
  fs.copyFileSync(path.join(app, "fnox.toml"), path.join(program, "fnox.toml"));
  manifest.workspace.infra = {
    backend: "remote",
    backends: { remote: { url: required("PULUMI_BACKEND_URL") } },
    environments: Object.fromEntries(
      ["dev", "stage", "prod", "preview"].map((env) => [env, { config }]),
    ),
  };
  manifest.workspace.deployment = YAML.parse(
    fs.readFileSync(path.join(here, "deployment.yaml"), "utf8"),
  );
  fs.writeFileSync(manifestFile, YAML.stringify(manifest));
  fs.appendFileSync(path.join(workspace, ".gitignore"), "\n.premise/\n.env*\n!.env.example\n");
  command(pm, ["infra", "bind", "web", "environment", "--artifact", "."], workspace);
  // Both apps use the same real TanStack fixture, with independent bindings.
  const admin = path.join(workspace, "apps/admin");
  fs.cpSync(app, admin, { recursive: true });
  const adminPackage = JSON.parse(fs.readFileSync(path.join(admin, "package.json"), "utf8"));
  adminPackage.name = "@premise/admin";
  fs.writeFileSync(path.join(admin, "package.json"), JSON.stringify(adminPackage));
  const clientManifest = YAML.parse(fs.readFileSync(manifestFile, "utf8"));
  clientManifest.workspace.projects.push({
    name: "admin",
    template: `${bunRegistry}:premise-tanstack-start-app`,
    path: "apps/admin",
  });
  fs.writeFileSync(manifestFile, YAML.stringify(clientManifest));
  command(pm, ["infra", "bind", "admin", "environment", "--artifact", "."], workspace);
  // Each declared branch profile points at the secrets owned by that preview.
  const currentBranch =
    process.env.PREMISE_PREVIEW_BRANCH ||
    process.env.GITHUB_HEAD_REF ||
    process.env.GITHUB_REF_NAME;
  const persisted = YAML.parse(fs.readFileSync(manifestFile, "utf8"));
  const appProject = persisted.workspace.projects.find((project) => project.name === "web");
  appProject.secrets.keys = ["APP_MARKER", "DATABASE_URL"];
  appProject.secrets.profiles = { preview: "preview-{branch}" };
  let appFnox = `[providers.gcp]\ntype = "gcp-sm"\nproject = "${required("GCP_PROJECT_ID")}"\n`;
  for (const branch of [currentBranch, currentBranch + "-isolation"]) {
    const plan = spawnSync(
      pm,
      ["deploy", "plan", "--env", "preview", "--branch", branch, "--json"],
      { cwd: workspace, env: process.env, encoding: "utf8" },
    );
    if (plan.status !== 0) throw new Error("Cannot resolve fixture preview identity");
    const stack = JSON.parse(plan.stdout).stack;
    appFnox += `\n[profiles.${stack}.secrets]\nAPP_MARKER = { provider = "gcp", value = "premise-e2e-${stack}-app-marker" }\nDATABASE_URL = { provider = "gcp", value = "premise-e2e-${stack}-database-url" }\n`;
  }
  fs.writeFileSync(path.join(app, "fnox.toml"), appFnox);
  fs.writeFileSync(manifestFile, YAML.stringify(persisted));
  command("mise", ["run", "install"], app);
  command("mise", ["run", "build"], app);
  return workspace;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) scaffold();
