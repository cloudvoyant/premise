# End-to-end tests

The CLI end-to-end suite lives under `e2e/cli/` using TypeScript and Vitest with hoisted `given ...` / `when ..., then ...` BDD specs. Every test runs the real `pm` binary against runnable workspace fixtures and retains its full transcript (`cli.log`), final workspace tree (`workspace/`), and manifest (`artifacts.json`) under git-ignored `e2e/outputs/cli/<spec>/<test-slug>/` for human and agent verification.

| Spec                | Lifecycle stage & coverage                                                                                    |
| ------------------- | ------------------------------------------------------------------------------------------------------------- |
| `workspace.spec.ts` | `pm init`, `pm generate` (Go, Cargo, Bun, polyglot, standalone Cargo), `pm install`, `pm template`, rollbacks |
| `secrets.spec.ts`   | Local JSON `fnox` profiles (`dev`, `stage`, `prod`, `preview`), missing-profile safety, `pm run`, GCP live    |
| `ci-checks.spec.ts` | `pm ci plan`, `on-commit` / `on-merge` task order, check and build failure gates, multi-OS matrix planning    |
| `release.spec.ts`   | Source-only releases, major action tags, private GitHub fixture proxy, release-destination permutations       |
| `deploy.spec.ts`    | `pm infra auth --ci` (GCP WIF + Azure `ci:auth`), Pulumi setup/plan/apply/query, preview rollback & promotion |

## Usage

```sh
mise run e2e                               # Offline replay suite (default for CI and local runs)
mise run e2e:cli:smoke                     # Fast representative smoke slice
mise run e2e:cli:list                      # List all BDD journeys without executing them
mise run e2e:cli:run -- workspace.spec.ts  # Run one spec file against bin/premise
mise run e2e:record -- release.spec.ts     # Run live against real providers and refresh redacted cassettes
mise run e2e:live -- deploy.spec.ts        # Run live against real providers without updating cassettes
mise run e2e:cleanup -- <artifacts.json>   # Tear down remote resources kept via PREMISE_E2E_KEEP_REMOTE
```

## Execution modes

`PREMISE_E2E_MODE` selects one of three deterministic modes:

- `replay` (default): Runs strictly offline against committed redacted JSON cassettes under `e2e/recordings/cli/`. Unrecorded HTTP requests fail closed; live cloud provisioning tests are skipped in CI.
- `record`: Runs locally against live providers (`cloudvoyant/premise-e2e` and dedicated GCP project `cloudvoyant-premise`), redacts auth headers and secret tokens, and writes updated cassettes under `e2e/recordings/cli/`.
- `live`: Runs locally against live providers without modifying committed cassettes.

## Inspecting test outputs

After any passing or failing run, inspect `e2e/outputs/cli/<spec>/<test-slug>/`:

- `cli.log`: Every `$ pm ...`, `$ mise ...`, and `$ git ...` command with working directory, exit code, stdout, and stderr.
- `workspace/`: The exact files left on disk after CLI operations finished.
- `artifacts.json`: SHA256 hashes of workspace files, remote resource metadata, consumer verification output, and the exact cleanup command when `PREMISE_E2E_KEEP_REMOTE` is set.

## Live provider fixtures

Live and record runs never run in CI. They target the permanent private repository `cloudvoyant/premise-e2e` (verified `private: true` before any write) and the dedicated GCP project `cloudvoyant-premise` (GCS state bucket `gs://cloudvoyant-premise-pulumi-state-547304404433`, Artifact Registry repositories `premise-e2e-npm`, `premise-e2e-go`, `premise-e2e-oci`, and Secret Manager secret `premise-e2e-test-secret`).

Set `PREMISE_E2E_KEEP_REMOTE=1` (or a test-name regex) to preserve live remote resources after a run for manual inspection, then tear them down with `mise run e2e:cleanup -- e2e/outputs/cli/<spec>/<test-slug>/artifacts.json`.
