import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import type { ArtifactManifest } from "./premise-harness.js";
import { destroyPulumiStack } from "../tsutils/gcsx.js";
import { deleteGithubBranch, deleteGithubReleaseAndTag } from "../tsutils/ghx.js";

async function main() {
  const target = process.argv[2];
  if (!target) {
    console.error("Usage: bun common/cleanup.ts <path/to/artifacts.json>");
    process.exit(1);
  }
  const manifestPath = resolve(target);
  const manifest = JSON.parse(await readFile(manifestPath, "utf8")) as ArtifactManifest;
  const { remote, workspaceDir } = manifest;

  if (remote.githubRepo && remote.tag) {
    console.log(`Deleting GitHub release/tag ${remote.tag} in ${remote.githubRepo}...`);
    await deleteGithubReleaseAndTag(remote.githubRepo, remote.tag);
  }
  if (remote.githubRepo && remote.branch) {
    console.log(`Deleting GitHub branch ${remote.branch} in ${remote.githubRepo}...`);
    await deleteGithubBranch(remote.githubRepo, remote.branch);
  }
  for (const stack of remote.pulumiStacks ?? []) {
    console.log(`Destroying Pulumi stack ${stack.stack} (${stack.backendUrl})...`);
    await destroyPulumiStack({
      infraDir: join(workspaceDir, "infra"),
      stack: stack.stack,
      backendUrl: stack.backendUrl,
      env: process.env,
    });
  }
  console.log(`Cleanup complete for ${manifest.testName}.`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
