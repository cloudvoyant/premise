import { existsSync } from "node:fs";
import { appendFile, chmod, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { collectLocalFiles, copyScaffold, slugify } from "../tsutils/fs.js";
import { execProcess, type CommandResult } from "../tsutils/process.js";
import { e2eMode, shouldKeepRemote, type E2EMode } from "./mode.js";

// Public API -----------------------------------------------------------------

export const REPO_ROOT = resolve(import.meta.dirname, "../..");
export const E2E_ROOT = join(REPO_ROOT, "e2e");
export const TESTDATA_ROOT = join(E2E_ROOT, "testdata");
export const OUTPUTS_ROOT = join(E2E_ROOT, "outputs");
export const RECORDINGS_ROOT = join(E2E_ROOT, "recordings");

export interface TestTaskMeta {
  filepath: string;
  name: string;
  fullName: string;
}

export interface RunOptions {
  cwd?: string;
  env?: Record<string, string | undefined>;
  input?: string;
  timeoutMs?: number;
  expectSuccess?: boolean;
}

export interface ArtifactManifest {
  testName: string;
  spec: string;
  mode: E2EMode;
  runId: string;
  workspaceDir: string;
  cliLogPath: string;
  localFiles: Array<{ path: string; sha256: string; bytes: number }>;
  remote: {
    retained: boolean;
    githubRepo?: string;
    branch?: string;
    tag?: string;
    releaseUrl?: string;
    registryPackages?: string[];
    gcpProject?: string;
    pulumiStacks?: Array<{ env: string; stack: string; backendUrl: string; bucket?: string }>;
  };
  consumerVerification?: { command: string; exitCode: number; stdout: string };
  cleanupCommand?: string;
}

export function resolvePremiseBinary(): string {
  const explicit = process.env.PREMISE_BIN?.trim();
  if (explicit) return resolve(explicit);
  const built = join(REPO_ROOT, "bin", process.platform === "win32" ? "premise.exe" : "premise");
  if (existsSync(built)) return built;
  const fallback = join(REPO_ROOT, "bin", "pm");
  if (existsSync(fallback)) return fallback;
  throw new Error(`Premise binary not found at ${built}. Run "mise run build" or set PREMISE_BIN.`);
}

/**
 * Owns one isolated, human- and agent-inspectable CLI E2E workspace under
 * e2e/outputs/cli/<spec>/<test-slug>/. Local outputs are always retained after
 * both passing and failing runs; remote cleanup honors PREMISE_E2E_KEEP_REMOTE.
 */
export class PremiseHarness {
  readonly env: NodeJS.ProcessEnv;
  readonly binary: string;
  readonly runId: string;
  readonly mode: E2EMode;
  readonly outputDir: string;
  readonly workspace: string;
  readonly cliLogPath: string;
  readonly manifestPath: string;
  readonly keepRemote: boolean;
  readonly remoteCleanups: Array<() => Promise<void>> = [];
  private manifestState: ArtifactManifest;

  private constructor(
    readonly task: TestTaskMeta,
    specRel: string,
    outputDir: string,
    mode: E2EMode,
    runId: string,
  ) {
    this.binary = resolvePremiseBinary();
    this.mode = mode;
    this.runId = runId;
    this.outputDir = outputDir;
    this.workspace = join(outputDir, "workspace");
    this.cliLogPath = join(outputDir, "cli.log");
    this.manifestPath = join(outputDir, "artifacts.json");
    this.keepRemote = shouldKeepRemote(task.fullName);
    this.env = {
      ...process.env,
      MISE_TRUSTED_CONFIG_PATHS: outputDir,
      PULUMI_HOME: join(outputDir, ".pulumi-home"),
      GIT_CEILING_DIRECTORIES: outputDir,
    };
    this.manifestState = {
      testName: task.fullName,
      spec: specRel,
      mode,
      runId,
      workspaceDir: this.workspace,
      cliLogPath: this.cliLogPath,
      localFiles: [],
      remote: { retained: this.keepRemote },
    };
  }

  static async create(options: {
    task: TestTaskMeta;
    fixture?: string;
    mode?: E2EMode;
    env?: Record<string, string | undefined>;
  }): Promise<PremiseHarness> {
    const mode = options.mode ?? e2eMode();
    const relSpec = relative(E2E_ROOT, options.task.filepath)
      .replaceAll("\\", "/")
      .replace(/\.spec\.ts$/, "");
    const slug = slugify(options.task.name) || "test";
    const outputDir = join(OUTPUTS_ROOT, relSpec, slug);
    await rm(outputDir, { recursive: true, force: true });
    await mkdir(join(outputDir, "workspace"), { recursive: true });
    await mkdir(join(outputDir, ".pulumi-home"), { recursive: true });

    const runId = `${Date.now()}-${Math.floor(Math.random() * 1000)}`;
    const harness = new PremiseHarness(options.task, relSpec, outputDir, mode, runId);
    if (options.env) {
      for (const [k, v] of Object.entries(options.env)) {
        if (v === undefined) delete harness.env[k];
        else harness.env[k] = v;
      }
    }
    await writeFile(
      harness.cliLogPath,
      `# Test: ${options.task.fullName}\n# Spec: e2e/${relSpec}.spec.ts\n# Mode: ${mode}\n# Workspace: ${harness.workspace}\n`,
      "utf8",
    );
    if (options.fixture) {
      await copyScaffold(join(TESTDATA_ROOT, options.fixture), harness.workspace);
    }
    return harness;
  }

  async run(program: string, args: string[], options: RunOptions = {}): Promise<CommandResult> {
    const cwd = options.cwd ?? this.workspace;
    const mergedEnv: NodeJS.ProcessEnv = { ...this.env };
    if (options.env) {
      for (const [k, v] of Object.entries(options.env)) {
        if (v === undefined) delete mergedEnv[k];
        else mergedEnv[k] = v;
      }
    }
    const existingTrusted = mergedEnv.MISE_TRUSTED_CONFIG_PATHS ?? "";
    if (!existingTrusted.split(":").includes(cwd)) {
      mergedEnv.MISE_TRUSTED_CONFIG_PATHS = existingTrusted ? `${existingTrusted}:${cwd}` : cwd;
    }

    const result = await execProcess(program, args, {
      cwd,
      env: mergedEnv,
      input: options.input,
      timeoutMs: options.timeoutMs,
    });

    const label = program === this.binary ? "pm" : program;
    const lines = [
      "",
      "================================================================================",
      `$ ${label} ${args.map(formatArg).join(" ")}`,
      `[cwd ${relative(this.outputDir, cwd) || "."}] [exit ${result.code ?? "null"}]`,
    ];
    if (result.stdout.trimEnd()) lines.push(result.stdout.trimEnd());
    if (result.stderr.trimEnd()) lines.push("--- stderr ---", result.stderr.trimEnd());
    await appendFile(this.cliLogPath, `${lines.join("\n")}\n`, "utf8");

    if (options.expectSuccess && result.code !== 0) {
      throw new Error(
        `Command failed (${label} ${args.join(" ")}, exit ${result.code}):\n${result.stdout}\n${result.stderr}`,
      );
    }
    return result;
  }

  pm(args: string[], options: RunOptions = {}): Promise<CommandResult> {
    return this.run(this.binary, args, { expectSuccess: true, ...options });
  }

  pmFail(args: string[], options: RunOptions = {}): Promise<CommandResult> {
    return this.run(this.binary, args, { expectSuccess: false, ...options });
  }

  mise(args: string[], options: RunOptions = {}): Promise<CommandResult> {
    return this.run("mise", args, { expectSuccess: true, ...options });
  }

  git(args: string[], options: RunOptions = {}): Promise<CommandResult> {
    return this.run("git", args, { expectSuccess: true, ...options });
  }

  async initGit(initialCommitMessage?: string, initialTag?: string): Promise<void> {
    await this.git(["init", "--quiet", "--initial-branch=main"]);
    await this.git(["config", "user.email", "e2e@premise.invalid"]);
    await this.git(["config", "user.name", "Premise E2E"]);
    if (initialCommitMessage) {
      await this.git(["add", "."]);
      await this.git(["commit", "--quiet", "-m", initialCommitMessage]);
      if (initialTag) {
        await this.git(["tag", initialTag]);
      }
    }
  }

  async writeFile(relPath: string, content: string | Buffer, mode?: number): Promise<string> {
    const full = join(this.workspace, relPath);
    await mkdir(dirname(full), { recursive: true });
    await writeFile(full, content, mode ? { mode } : undefined);
    if (mode !== undefined) {
      await chmod(full, mode);
    }
    return full;
  }

  async readFile(relPath: string): Promise<string> {
    return readFile(join(this.workspace, relPath), "utf8");
  }

  registerRemoteCleanup(fn: () => Promise<void>): void {
    this.remoteCleanups.push(fn);
  }

  updateRemoteManifest(patch: Partial<ArtifactManifest["remote"]>): void {
    this.manifestState.remote = {
      ...this.manifestState.remote,
      ...patch,
    };
  }

  async writeManifest(): Promise<string> {
    this.manifestState.localFiles = await collectLocalFiles(this.workspace);
    if (this.keepRemote) {
      this.manifestState.remote.retained = true;
      this.manifestState.cleanupCommand = `mise run e2e:cleanup -- ${relative(REPO_ROOT, this.manifestPath)}`;
    }
    await writeFile(this.manifestPath, `${JSON.stringify(this.manifestState, null, 2)}\n`, "utf8");
    return this.manifestPath;
  }

  async dispose(): Promise<void> {
    if (!this.keepRemote) {
      for (const cleanup of [...this.remoteCleanups].reverse()) {
        await cleanup();
      }
    }
    await this.writeManifest();
  }
}

// Private helpers ------------------------------------------------------------

function formatArg(arg: string): string {
  return /[\s"']/.test(arg) ? JSON.stringify(arg) : arg;
}
