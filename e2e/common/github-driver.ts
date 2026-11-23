import { execProcess } from "./premise-harness.js";

export const DEFAULT_GITHUB_FIXTURE_REPO = "cloudvoyant/premise-e2e";

export function fixtureGithubRepo(): string {
  return process.env.PREMISE_E2E_GITHUB_REPO?.trim() || DEFAULT_GITHUB_FIXTURE_REPO;
}

/** Verify the permanent GitHub fixture repository exists and is strictly private before any write. */
export async function assertPrivateFixtureRepo(repo = fixtureGithubRepo()): Promise<void> {
  const res = await execProcess(
    "gh",
    ["repo", "view", repo, "--json", "nameWithOwner,visibility,isPrivate"],
    { cwd: process.cwd(), env: process.env, timeoutMs: 30_000 },
  );
  if (res.code !== 0) {
    throw new Error(`Unable to inspect GitHub fixture repo ${repo}: ${res.combined}`);
  }
  const parsed = JSON.parse(res.stdout) as { isPrivate?: boolean; visibility?: string };
  if (!parsed.isPrivate && parsed.visibility !== "PRIVATE") {
    throw new Error(`Refusing to write to non-private fixture repository ${repo}`);
  }
}

export async function deleteGithubReleaseAndTag(repo: string, tag: string): Promise<void> {
  await execProcess("gh", ["release", "delete", tag, "--repo", repo, "--yes", "--cleanup-tag"], {
    cwd: process.cwd(),
    env: process.env,
    timeoutMs: 30_000,
  });
  await execProcess("gh", ["api", "--method", "DELETE", `repos/${repo}/git/refs/tags/${tag}`], {
    cwd: process.cwd(),
    env: process.env,
    timeoutMs: 30_000,
  });
}

export async function deleteGithubBranch(repo: string, branch: string): Promise<void> {
  await execProcess("gh", ["api", "--method", "DELETE", `repos/${repo}/git/refs/heads/${branch}`], {
    cwd: process.cwd(),
    env: process.env,
    timeoutMs: 30_000,
  });
}
