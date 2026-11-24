# End-to-end tests

The CLI end-to-end suite lives under `e2e/cli/` using TypeScript and Vitest with hoisted `Given ...` / `When ...` / `It ...` BDD specs. Every test copies a real scaffolded workspace from `e2e/testdata/` into git-ignored `e2e/outputs/cli/<spec>/<test-slug>/workspace/`, runs the real `pm` binary, and retains `cli.log` and `artifacts.json` alongside the final workspace tree for human and agent verification.

| Spec                  | Command or flow coverage                                                                                           |
| --------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `scaffolding.spec.ts` | `pm init`, `pm generate` (Go, Cargo, Bun, polyglot, shared root merges), `pm install`, `pm template`, rollbacks    |
| `secrets.spec.ts`     | `pm secrets` matrixed over secret providers (`local-json` and `gcp-secret-manager`), 0600 `.env.local`, `pm run`   |
| `infra.spec.ts`       | `pm infra` (`auth`, `setup`, `plan`, `apply`, `query`, `destroy`) branched by `Given no prior infra` / `existing`  |
| `release.spec.ts`     | `pm release` (source-only dry-run/build/retry/RC/action steps, major action tags, private repo, pkg permutations)  |
| `on-commit.spec.ts`   | `on-commit` flow across generic (with/without infra) and `bun`, `rust`, `go` workspaces (checks, staging, preview) |
| `on-merge.spec.ts`    | `on-merge` flow across generic (with/without infra) and `bun`, `rust`, `go` workspaces (version bump, cleanup)     |
| `on-deploy.spec.ts`   | `on-deploy` flow (immutable release promotion across `dev`, `stage`, `prod` with migrate -> deploy -> health)      |

## Usage

```sh
mise run e2e                                 # Offline replay suite (default for CI and local runs)
mise run e2e:cli:smoke                       # Fast representative smoke slice
mise run e2e:cli:list                        # List all BDD journeys without executing them
mise run e2e:cli:run -- scaffolding.spec.ts  # Run one spec file against bin/premise
mise run e2e:record -- release.spec.ts       # Run live against real providers and refresh redacted cassettes
mise run e2e:live -- infra.spec.ts           # Run live against real providers without updating cassettes
mise run e2e:cleanup -- <artifacts.json>     # Tear down remote resources kept via PREMISE_E2E_KEEP_REMOTE
```

## Execution modes

`PREMISE_E2E_MODE` selects one of three deterministic modes:

- `replay` (default): Runs strictly offline against committed redacted JSON cassettes under `e2e/recordings/cli/`. Unrecorded HTTP requests fail closed; live cloud provisioning tests are skipped in CI.
- `record`: Runs locally against live providers (`cloudvoyant/premise-e2e` and dedicated GCP project `cloudvoyant-premise`), redacts auth headers and secret tokens, and writes updated cassettes under `e2e/recordings/cli/`.
- `live`: Runs locally against live providers without modifying committed cassettes.

## Inspecting test outputs

After any passing or failing run, inspect `e2e/outputs/cli/<spec>/<test-slug>/`:

- `cli.log`: Every `$ pm ...`, `$ mise ...`, and `$ git ...` command with working directory, exit code, stdout, and stderr.
- `workspace/`: The exact scaffold copied from `e2e/testdata/` after CLI operations finished.
- `artifacts.json`: SHA256 hashes of workspace files, remote resource metadata, consumer verification output, and the exact cleanup command when `PREMISE_E2E_KEEP_REMOTE` is set.

## Live provider fixtures

Live and record runs never run in CI. They target the permanent private repository `cloudvoyant/premise-e2e` (verified `private: true` before any write) and the dedicated GCP project `cloudvoyant-premise` (GCS state bucket `gs://cloudvoyant-premise-pulumi-state-547304404433`, Artifact Registry repositories `premise-e2e-npm`, `premise-e2e-go`, `premise-e2e-oci`, and Secret Manager secret `premise-e2e-test-secret`).

Set `PREMISE_E2E_KEEP_REMOTE=1` (or a test-name regex) to preserve live remote resources after a run for manual inspection, then tear them down with `mise run e2e:cleanup -- e2e/outputs/cli/<spec>/<test-slug>/artifacts.json`.
