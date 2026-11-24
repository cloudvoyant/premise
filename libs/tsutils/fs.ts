import { createHash } from "node:crypto";
import { cp, mkdir, readdir, readFile, stat } from "node:fs/promises";
import { join, relative } from "node:path";

// Public API -----------------------------------------------------------------

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

export function hostCIPlatform(): "linux" | "macos" | "windows" {
  if (process.platform === "darwin") return "macos";
  if (process.platform === "win32") return "windows";
  return "linux";
}

export async function pathExists(path: string): Promise<boolean> {
  try {
    await stat(path);
    return true;
  } catch {
    return false;
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
