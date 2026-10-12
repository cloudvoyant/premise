import { spawnSync } from "node:child_process";
import * as path from "node:path";
const result = spawnSync(
  "pulumi",
  ["destroy", "--stack", process.env.PM_STACK, "--yes", "--non-interactive"],
  { cwd: path.resolve("../../infra/environment"), stdio: "inherit" },
);
if (result.status !== 0) throw new Error("First-deployment infrastructure cleanup failed");
