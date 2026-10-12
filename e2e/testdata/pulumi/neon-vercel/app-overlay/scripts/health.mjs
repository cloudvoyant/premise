const headers = process.env.PREMISE_E2E_BYPASS_SECRET
  ? { "x-vercel-protection-bypass": process.env.PREMISE_E2E_BYPASS_SECRET }
  : {};
let servingExpectedRelease = false;
for (let attempt = 0; attempt < 30; attempt++) {
  try {
    const response = await fetch(`${process.env.APP_URL}/api/e2e`, {
      signal: AbortSignal.timeout(10000),
      headers,
    });
    if (response.ok && (await response.json()).release === process.env.PM_RELEASE) {
      servingExpectedRelease = true;
      break;
    }
  } catch {
    /* Wait for provider/DNS readiness within the bounded health window. */
  }
  await new Promise((resolve) => setTimeout(resolve, 1000));
}
if (!servingExpectedRelease) throw new Error("Expected release did not become healthy");
console.log(`Serving release ${process.env.PM_RELEASE} verified`);
// This fault occurs only after the new app answered its database-backed endpoint.
if (process.env.PREMISE_E2E_FAIL_HEALTH === "1")
  throw new Error("Intentional post-rollout health failure");
