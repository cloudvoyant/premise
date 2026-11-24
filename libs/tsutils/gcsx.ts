import { createHash } from "node:crypto";
import { execProcess } from "./process.js";

// Public API -----------------------------------------------------------------

export const DEFAULT_GCP_PROJECT = "cloudvoyant-premise";
export const DEFAULT_PULUMI_STATE_BUCKET = "gs://cloudvoyant-premise-pulumi-state-547304404433";
export const DEFAULT_GAR_LOCATION = "us-central1";

export interface LiveGcpConfig {
  project: string;
  backendUrl: string;
  garLocation: string;
}

export function resolveLiveGcpConfig(): LiveGcpConfig {
  const project = process.env.PREMISE_TEST_GCP_PROJECT?.trim() || DEFAULT_GCP_PROJECT;
  const backendUrl = process.env.PULUMI_BACKEND_URL?.trim() || DEFAULT_PULUMI_STATE_BUCKET;
  if (!backendUrl.startsWith("gs://")) {
    throw new Error(`PULUMI_BACKEND_URL must start with gs://, got "${backendUrl}"`);
  }
  return {
    project,
    backendUrl,
    garLocation: process.env.PREMISE_TEST_GAR_LOCATION?.trim() || DEFAULT_GAR_LOCATION,
  };
}

export function patchManifestInfra(
  manifest: string,
  options: { primaryBackend: string; alternateBackend: string; gcpProject: string },
): string {
  const indent = manifest.match(/^([ ]+)name:/m)?.[1] ?? "  ";
  const i2 = indent + indent;
  const i3 = i2 + indent;
  const i4 = i3 + indent;
  const block = [
    `${indent}infra:`,
    `${i2}directory: infra/env`,
    `${i2}shared_directory: infra/shared`,
    `${i2}backend: primary`,
    `${i2}backends:`,
    `${i3}primary:`,
    `${i4}url: ${options.primaryBackend}`,
    `${i3}alternate:`,
    `${i4}url: ${options.alternateBackend}`,
    `${i2}environments:`,
    `${i3}shared:`,
    `${i4}backend: primary`,
    `${i4}config:`,
    `${i4}${indent}gcp:project: ${options.gcpProject}`,
    `${i3}dev:`,
    `${i4}backend: primary`,
    `${i4}config:`,
    `${i4}${indent}gcp:project: ${options.gcpProject}`,
    `${i3}stage:`,
    `${i4}backend: primary`,
    `${i4}config:`,
    `${i4}${indent}gcp:project: ${options.gcpProject}`,
    `${i3}prod:`,
    `${i4}backend: alternate`,
    `${i4}config:`,
    `${i4}${indent}gcp:project: ${options.gcpProject}`,
    `${i3}preview:`,
    `${i4}backend: alternate`,
    `${i4}config:`,
    `${i4}${indent}gcp:project: ${options.gcpProject}`,
  ].join("\n");

  const existingRe = new RegExp(`^${indent}infra:\\n(?:${indent}[ ].*\\n?)*`, "m");
  if (existingRe.test(manifest)) {
    return manifest.replace(existingRe, `${block}\n`);
  }
  return `${manifest.trimEnd()}\n${block}\n`;
}

export async function destroyPulumiStack(options: {
  infraDir: string;
  stack: string;
  backendUrl: string;
  env: NodeJS.ProcessEnv;
}): Promise<void> {
  const res = await execProcess(
    "mise",
    ["exec", "--", "pulumi", "destroy", "--stack", options.stack, "--yes", "--non-interactive"],
    {
      cwd: options.infraDir,
      env: {
        ...options.env,
        PULUMI_BACKEND_URL: options.backendUrl,
        PULUMI_CONFIG_PASSPHRASE: "",
      },
      timeoutMs: 300_000,
    },
  );
  if (res.code !== 0 && !res.combined.includes("no stack named")) {
    throw new Error(`Failed to destroy Pulumi stack ${options.stack}: ${res.combined}`);
  }
}

export async function assertGcsBucketDeleted(bucket: string, cwd: string): Promise<void> {
  const res = await execProcess("gcloud", ["storage", "buckets", "describe", `gs://${bucket}`], {
    cwd,
    env: process.env,
    timeoutMs: 30_000,
  });
  if (res.code === 0 || !res.combined.includes("404")) {
    throw new Error(`Expected bucket gs://${bucket} to be deleted (404), got: ${res.combined}`);
  }
}

export async function readGcsObject(bucket: string, object: string, cwd: string): Promise<string> {
  const res = await execProcess("gcloud", ["storage", "cat", `gs://${bucket}/${object}`], {
    cwd,
    env: process.env,
    timeoutMs: 30_000,
  });
  if (res.code !== 0) {
    throw new Error(`Failed to read gs://${bucket}/${object}: ${res.combined}`);
  }
  return res.stdout;
}

export async function readGcsBucketLabel(
  bucket: string,
  label: string,
  cwd: string,
): Promise<string> {
  const res = await execProcess(
    "gcloud",
    ["storage", "buckets", "describe", `gs://${bucket}`, `--format=value(labels.${label})`],
    { cwd, env: process.env, timeoutMs: 30_000 },
  );
  if (res.code !== 0) {
    throw new Error(`Failed to describe gs://${bucket}: ${res.combined}`);
  }
  return res.stdout.trim();
}

export async function assertSavedPulumiState(options: {
  backendUrl: string;
  env: "shared" | "dev" | "stage" | "prod" | "preview";
  branch?: string;
  cwd: string;
}): Promise<void> {
  const project = options.env === "shared" ? "premise-gcs-fixture-shared" : "premise-gcs-fixture";
  const stack =
    options.env === "preview"
      ? `preview-${(options.branch ?? "").replace(/^feature\//, "")}-${createHash("sha256")
          .update(options.branch ?? "")
          .digest("hex")
          .slice(0, 10)}`
      : options.env;
  const statePath = `${options.backendUrl}/.pulumi/stacks/${project}/${stack}.json`;
  const res = await execProcess(
    "gcloud",
    ["storage", "objects", "describe", statePath, "--format=value(name)"],
    { cwd: options.cwd, env: process.env, timeoutMs: 30_000 },
  );
  if (res.code !== 0) {
    throw new Error(`Expected Pulumi state at ${statePath}: ${res.combined}`);
  }
}
