const previous = JSON.parse(process.env.PM_PREVIOUS_OUTPUTS ?? "{}");
if (!previous.deploymentId || !previous.aliasName)
  throw new Error("Previous deployment identity is missing");
if (process.env.PREMISE_E2E_FAIL_RECOVERY === "1")
  throw new Error("Intentional recovery-hook failure");
for (const [deploymentId, aliasName] of [
  [previous.deploymentId, previous.aliasName],
  [previous.adminDeploymentId, previous.adminAliasName],
]) {
  if (!deploymentId || !aliasName) throw new Error("Previous app identity is missing");
  const url = new URL(`https://api.vercel.com/v2/deployments/${deploymentId}/aliases`);
  url.searchParams.set("teamId", process.env.VERCEL_TEAM_ID);
  const response = await fetch(url, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${process.env.VERCEL_API_TOKEN ?? process.env.VERCEL_TOKEN}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ alias: aliasName }),
  });
  if (!response.ok) throw new Error(`Vercel rollback HTTP ${response.status}`);
}
for (let attempt = 0; attempt < 30; attempt++) {
  const check = await fetch(`${previous.appURL}/api/e2e`, {
    signal: AbortSignal.timeout(10000),
    headers: process.env.PREMISE_E2E_BYPASS_SECRET
      ? { "x-vercel-protection-bypass": process.env.PREMISE_E2E_BYPASS_SECRET }
      : {},
  });
  if (check.ok && (await check.json()).release === process.env.PM_PREVIOUS_RELEASE) process.exit(0);
  await new Promise((resolve) => setTimeout(resolve, 1000));
}
throw new Error("Previous release did not become healthy after rollback");
