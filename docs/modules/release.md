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
| `GoReleaserConfig(root)`                             | Generate the merged temporary GoReleaser configuration.                |
| `ReleasePlan`                                        | Return the selected version plus skip/reuse decisions.                 |

### Usage

```go
plan, err := core.PrepareStableRelease(ctx, root, stdout)
if err != nil || plan.Skip {
    return err
}
_, err = core.PublishGitHubRelease(ctx, root, stdout, stderr)
```

Use the narrower prepare/GitHub/packages APIs in separate credential-bearing CI steps. Use `PublishStableRelease` when one process owns all credentials.

## Implementation Details

Release loads `premise.yaml`, resolves explicitly selected registered plugins, and rejects missing or conflicting managers before release work. It asks each selected plugin for artifact fragments, merges `builds` and `archives`, and rejects unknown sections and duplicate artifact IDs.

`PlanStableRelease` fetches tags, checks for a stable tag at HEAD, and compares current and next versions. `PrepareStableRelease` creates and pushes a tag only when needed; a failed push removes the local tag.

GoReleaser configuration is written to a temporary file. Release calls the Mise subprocess boundary directly to resolve a pinned GoReleaser tool environment; it does not call the public Task module entry points. Package-registry credentials are removed, while GitHub credentials are added explicitly. Temporary configuration is removed after execution.

Plugins that contribute artifacts must agree on one release workspace. Their serial requirements are combined; Cargo requests serial GoReleaser execution after preparing its toolchain.

Language-package credentials are excluded from general Mise and GoReleaser environments. Plugins receive only the credentials needed for their publication subprocesses. Context cancellation propagates to Git operations, Mise, registry checks, and GoReleaser.
