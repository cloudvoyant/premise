import { createHash } from "node:crypto";
import { cp, mkdir, readdir, readFile, stat } from "node:fs/promises";
import { join, relative } from "node:path";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Json = Record<string, any>;

export type E2EMode = "replay" | "record" | "live";

export function e2eMode(): E2EMode {
  const raw = (process.env.PREMISE_E2E_MODE ?? "replay").trim().toLowerCase();
  if (raw === "replay" || raw === "record" || raw === "live") return raw;
  throw new Error(`Invalid PREMISE_E2E_MODE "${raw}": expected replay, record, or live`);
}

export function isReplayMode(): boolean {
  return e2eMode() === "replay";
}

export function isLiveOrRecordMode(): boolean {
  const mode = e2eMode();
  return mode === "live" || mode === "record";
}

export async function pathExists(path: string): Promise<boolean> {
  try {
    await stat(path);
    return true;
  } catch {
    return false;
  }
}

export function sha256(bytes: string | Buffer): string {
  return createHash("sha256").update(bytes).digest("hex");
}

export function slugify(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .slice(0, 96);
}

export function shouldKeepRemote(testFullName: string): boolean {
  const filter = (process.env.PREMISE_E2E_KEEP_REMOTE ?? "").trim();
  if (!filter || filter === "0" || filter === "false") return false;
  if (filter === "1" || filter === "true" || filter === "all") return true;
  try {
    return new RegExp(filter, "i").test(testFullName);
  } catch {
    return testFullName.toLowerCase().includes(filter.toLowerCase());
  }
}

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
