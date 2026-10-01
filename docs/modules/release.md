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
pm release --dry-run   # report the plan without changing the repository
pm release --build     # build release artifacts without publishing
```

## Implementation Details

Release loads `premise.yaml`, resolves explicitly selected registered backends, and rejects missing or conflicting managers before release work. It asks each selected backend for artifact fragments, merges `builds` and `archives`, and rejects unknown sections and duplicate artifact IDs.

`PlanStableRelease` reads tags and compares the current version with the next version. It reuses a stable tag at `HEAD`. When no stable tag exists, it uses `v0.0.0` as the version baseline and plans the first feature release as `v0.1.0`.

GoReleaser configuration is written to a temporary file. Release calls the Mise subprocess boundary directly to resolve a pinned GoReleaser tool environment; it does not call the public Task module entry points. Package-registry credentials are removed, while GitHub credentials are added explicitly. Temporary configuration is removed after execution.

`BuildReleaseArtifacts` uses GoReleaser snapshot mode. It builds the configured artifacts but neither tags nor publishes them. If no backend contributes downloadable artifacts, it skips the build.

Backends that contribute artifacts must agree on one release workspace. Their serial requirements are combined; Cargo requests serial GoReleaser execution after preparing its toolchain.

Language-package credentials are excluded from general Mise and GoReleaser environments. Backends receive only the credentials needed for their publication subprocesses. Missing or invalid credentials fail before publication; correct them and rerun, reusing the existing tag. Context cancellation propagates to Git operations, Mise, registry checks, and GoReleaser.
