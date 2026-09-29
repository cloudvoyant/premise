# Release Module

## Purpose

The release module plans repository releases, creates and reuses stable tags, combines selected package-manager artifacts, publishes GitHub releases, and invokes language-package publication in a defined order.

## Design

### API

| API                                                  | Use                                                                    |
| ---------------------------------------------------- | ---------------------------------------------------------------------- |
| `PlanStableRelease(ctx, root)`                       | Decide whether to skip, create a tag, or reuse the stable tag at HEAD. |
| `PrepareStableRelease(ctx, root, stdout)`            | Plan and create/push a missing stable tag.                             |
| `PublishGitHubRelease(ctx, root, stdout, stderr)`    | Publish downloadable artifacts for a prepared tag.                     |
| `PublishLanguagePackages(ctx, root, stdout, stderr)` | Publish eligible registry packages for a prepared tag.                 |
| `PublishStableRelease(ctx, root, stdout, stderr)`    | Prepare, publish GitHub artifacts, then publish packages.              |
| `BuildReleaseArtifacts(ctx, root, stdout, stderr)`   | Build the complete artifact matrix without tags or publication.        |
| `GoReleaserConfig(root)`                             | Return the merged GoReleaser configuration as bytes.                   |
| `ReleasePlan`                                        | Return the selected version plus skip/reuse decisions.                 |

### Usage

```bash
pm release             # prepare and publish the stable release
pm release --dry-run   # report version and should_publish without mutation
pm release --build     # build release artifacts locally without publishing
```

The CLI does not expose `prepare`, `github`, `packages`, or `snapshot` subcommands. For CI jobs with separate credentials, use `PrepareStableRelease`, `PublishGitHubRelease`, and `PublishLanguagePackages` from Go. `pm ci flow on-merge` can also select `github` and `packages` release phases. `PublishStableRelease` performs all three steps when one process owns all credentials. The sequence is plan, resolve, structural preflight, full preflight, tag, GitHub artifacts, then language packages.

## Implementation Details

Release loads `premise.yaml`, resolves explicitly selected registered backends, and rejects missing or conflicting managers before release work. It asks each selected backend for artifact fragments, merges `builds` and `archives`, and rejects unknown sections and duplicate artifact IDs.

`PlanStableRelease` fetches tags, checks for a stable tag at HEAD, and compares current and next versions. With no stable tags, versioning uses a virtual `v0.0.0` baseline; the first feature release plans `v0.1.0` without creating a bootstrap ref. `PrepareStableRelease` creates and pushes a tag only after structural and full preflight succeed; a failed push removes the local tag. A rerun reuses a stable tag already at `HEAD`.

GoReleaser configuration is written to a temporary file. Release calls the Mise subprocess boundary directly to resolve a pinned GoReleaser tool environment; it does not call the public Task module entry points. Package-registry credentials are removed, while GitHub credentials are added explicitly. Temporary configuration is removed after execution.

`BuildReleaseArtifacts` uses GoReleaser snapshot mode. It builds the configured artifacts but neither tags nor publishes them. If no backend contributes downloadable artifacts, it skips the build.

Backends that contribute artifacts must agree on one release workspace. Their serial requirements are combined; Cargo requests serial GoReleaser execution after preparing its toolchain.

Language-package credentials are excluded from general Mise and GoReleaser environments. Backends receive only the credentials needed for their publication subprocesses. Missing or invalid credentials fail before publication; correct them and rerun, reusing the existing tag. Context cancellation propagates to Git operations, Mise, registry checks, and GoReleaser.
