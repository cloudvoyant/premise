import { spawn } from "node:child_process";

// Public API -----------------------------------------------------------------

export interface CommandResult {
  code: number | null;
  stdout: string;
  stderr: string;
  combined: string;
}

export async function execProcess(
  program: string,
  args: string[],
  options: {
    cwd: string;
    env: NodeJS.ProcessEnv;
    input?: string;
    timeoutMs?: number;
  },
): Promise<CommandResult> {
  const child = spawn(program, args, {
    cwd: options.cwd,
    env: options.env,
    stdio: ["pipe", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stdout.on("data", (chunk) => {
    stdout += String(chunk);
  });
  child.stderr.on("data", (chunk) => {
    stderr += String(chunk);
  });
  if (options.input !== undefined) child.stdin.end(options.input);
  else child.stdin.end();

  const timeoutMs = options.timeoutMs ?? 600_000;
  const timer = setTimeout(() => child.kill("SIGKILL"), timeoutMs);
  try {
    const code = await new Promise<number | null>((resolveCode, reject) => {
      child.once("error", reject);
      child.once("close", (exitCode) => resolveCode(exitCode));
    });
    return { code, stdout, stderr, combined: `${stdout}${stderr}` };
  } finally {
    clearTimeout(timer);
  }
}
