# CI Module

## Purpose

Premise owns project selection, task order, and release policy. GitHub Actions chooses runners, transfers native files between them, and isolates publishing credentials. A local flow works on the current host; GitHub schedules other operating systems when needed.

## Design

### Project selection

A registry template or workspace project can declare logical `check_platforms` and `release_platforms`. Omitted check platforms mean Linux; omitted release platforms mean no native release files. These fields do not name a build system. Premise uses the same declarations for `pm projects ls`, `pm ci plan`, and `pm ci flow`.

```yaml
ci:
  check_platforms: [linux, macos, windows]
  release_platforms: [linux, macos, windows]
```

Commands:

```text
pm projects ls --type app --flow on-merge --platform-mode multi --json
pm ci flow on-commit
pm ci flow on-commit --project desktop-app --platform macos
```

Without filters, the local flow runs host-compatible single-platform projects and reports excluded multi-platform projects. With `--project` and `--platform`, it runs the selected project's complete work for that declared target on a matching host. A platform-scoped flow can stage native files, but it never creates a workspace release on its own. A root Mise task is a once-per-invocation hook; it does not replace project work. Local release validation is mutation-free.

### Commit and merge

These are the hosted job shapes and commands. GitHub picks exactly one shape for a run. A single-runner job can perform all checks, quality tasks, and native staging in one `pm ci flow` invocation. If the selected check and release targets need multiple runner operating systems, GitHub expands one matrix over the union of declared project/platform targets. Single-platform projects are rows in that matrix too. The root hook runs once before matrix rows; each row performs the checks and/or native build declared for its target. Quality can run on more than one platform rather than requiring another job graph.

```text
+-------------------------- A. ALL WORK FITS ONE RUNNER OS --------------------------+
| flow job (no matrix)                                                               |
| pm ci flow <flow>                                |
| root once; projects; checks; quality; stage native files; dry release plan         |
| upload staged files if present                                                     |
+------------------------------------------------------------------------------------+
                                          |
                                          v publish only when requested
+--------------------------------- publish-release ----------------------------------+
| pm release publish --channel <rc|stable> --expected-version <v>                    |
| --files-dir <downloaded-files>                                           |
| validate all files before tag; publish archives + native files once                |
+------------------------------------------------------------------------------------+
                                          |
                                          v publication job succeeded
+--------------------------------- publish-packages ---------------------------------+
| pm release packages --channel <rc|stable> --expected-version <v>         |
+------------------------------------------------------------------------------------+

+----------------------- B. MORE THAN ONE RUNNER OS REQUIRED ------------------------+
| plan/root job                                                                      |
| pm ci plan --flow <flow> --github-output                                 |
| pm ci flow <flow> --root-only                                            |
+------------------------------------------------------------------------------------+
                                          |
                                          v one row per project/platform union
+------------------------------- flow [GitHub matrix] -------------------------------+
| pm ci flow <flow> --project <name> --platform <target>                             |
| --skip-root --output-dir <dir>                                           |
| run declared checks and/or native build; upload staged files if any                |
+------------------------------------------------------------------------------------+
                                          |
                                          v ALL rows passed; publish requested
+--------------------------------- publish-release ----------------------------------+
| download ALL row files; validate complete expected set before tag                  |
| pm release publish --channel <rc|stable> --expected-version <v>                    |
| --files-dir <downloaded-files>                                           |
+------------------------------------------------------------------------------------+
                                          |
                                          v publication job succeeded
+--------------------------------- publish-packages ---------------------------------+
| pm release packages --channel <rc|stable> --expected-version <v>         |
+------------------------------------------------------------------------------------+
```

If publication is not requested, the run stops after the flow job or matrix succeeds. When it is requested, all selected native builds must finish before the publication job validates their transferred files. That job creates or reuses a tag at the triggering commit and runs GoReleaser once with ordinary archives and native files. It rejects missing files and tags at another commit. The package job starts only after GitHub publication succeeds. Native files travel between jobs as CI artifacts, never as caches. Package credentials stay in the protected package job; the flow jobs receive none. Even a one-OS release can need separate publication jobs to protect credentials.

Tauri keeps its normal Mise tasks and Cargo build directories. Its `release:build` task copies output into the flow's artifact directory without tagging or uploading. Windows RC builds use NSIS; stable builds can also use MSI. No Bash release script is needed. Cache keys must separate incompatible runner images; a failed cross-image cache restore is not evidence of a speedup.

### Manual deploy

`on-release` stays independent of commit/merge publication. The root override or optional app deploy and end-to-end tasks receive `stage` or `prod`. Native builds use scoped commit or merge flows; the old `on-platform` flow and platform helper commands have been removed. Before premise-cargo merges, its temporary Premise branch reference must become an immutable released tag. The `v0` alias can move only after that release is verified.

## API

| Command                    | Responsibility                                                                                    |
| -------------------------- | ------------------------------------------------------------------------------------------------- |
| `pm ci flow <flow>`        | Run host-compatible tasks and stage files.                                                        |
| `pm projects ls`           | Explain eligible and excluded projects.                                                           |
| `pm ci plan --flow <flow>` | Describe the runner schedule as JSON with `--json`, or step output values with `--github-output`. |
| `pm release publish`       | Validate files and publish one combined GitHub Release.                                           |
| `pm release packages`      | Publish registry packages only after release success.                                             |

## Composite action

Commit and merge pipelines call the Premise action for each operation. GitHub still owns job dependencies, runner selection, and artifact transfer. The action's `command` input selects `plan`, `flow`, `flow-matrix`, `release`, or `packages`. `flow-matrix` requires a project and platform and uses the same complete flow as the single job.

```yaml
- uses: cloudvoyant/premise@v0
  id: plan
  with:
    command: plan
    flow: on-commit
    release: auto

- uses: cloudvoyant/premise@v0
  with:
    command: flow-matrix
    flow: on-commit
    project: ${{ matrix.project }}
    platform: ${{ matrix.platform }}
    skip-root: true
    channel: rc
    version: ${{ needs.plan.outputs.version }}
    output-dir: ${{ runner.temp }}/premise-files

- uses: cloudvoyant/premise@v0
  env:
    GH_TOKEN: ${{ github.token }}
  with:
    command: release
    channel: rc
    version: ${{ needs.plan.outputs.version }}
    files-dir: ${{ runner.temp }}/premise-files
```

The plan operation exports `mode`, `runner`, `matrix`, `native`, and `release_version`. The CLI writes the schedule directly to `GITHUB_OUTPUT`, without shell parsing. A marked feature-branch push (`[publish-rc]`) plans an RC; pull request checks do not publish. RC releases are GitHub prereleases and do not replace the latest stable release. The package job runs after successful publication even when the other runner shape was skipped.

The flow engine owns selection, task order, host validation, safe release planning, and file staging. The release publisher owns file validation, tag safety, one combined GitHub Release, and later package publication. GitHub Actions owns runner and matrix selection, job dependencies, artifact transfer, and credential boundaries. A green check or dry run does not prove publication: inspect the hosted tag, release, archives, native assets, and registry packages before claiming it succeeded.
