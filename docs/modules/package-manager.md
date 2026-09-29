# Package Manager Module

## Purpose

The package-manager module defines how selected package ecosystems expose metadata, validate packages, contribute artifacts, and publish language packages. Core owns the contract and orchestration; `core/backends` contains built-in Go, Cargo, and Bun implementations.

## Design

### API

| API                                                 | Use                                                                                                  |
| --------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `PackageManagerBackend.ID()`                        | Return the value accepted by `workspace.package_managers`.                                           |
| `PackageManagerBackend.Ecosystem()`                 | Return the incompatibility key, such as `npm`, `cargo`, or `go`.                                     |
| `GetPackageMetadata(root, template)`                | Parse one native package specification into `PackageMetadata`.                                       |
| `ValidatePackage(root, template)`                   | Perform static package validation.                                                                   |
| `WillPublishOk(ctx, root, template, version, task)` | Preflight one package without publication.                                                           |
| `CreateGoReleaserConfig(root, manifest)`            | Contribute GoReleaser `builds` and `archives`.                                                       |
| `SupportsPackages()`                                | Report whether the manager publishes a language registry package.                                    |
| `PublishPackages(ctx, root, version, task, ...)`    | Preflight and publish eligible packages.                                                             |
| `ReleaseWorkspace(ctx, root, ...)`                  | Prepare the workspace used for artifact builds.                                                      |
| `RegisterPackageManagerBackend(backend)`            | Register an implementation before invoking release APIs.                                             |
| `PackageMetadata`                                   | Carry manager-neutral package name, version, path, registry, visibility, and publication capability. |

### Usage

```go
if err := core.RegisterPackageManagerBackend(myBackend); err != nil {
    return err
}
```

```yaml
workspace:
  package_managers:
    - go
    - cargo
    - bun
```

The CLI registers built-ins in its composition root. Library clients register their own implementations explicitly; backends never self-register through `init`.

## Implementation Details

Backend selection uses only `workspace.package_managers`; native file presence never activates a backend. The selected manager list is the provenance of release policy and preserves declaration order. Publication targets are then derived from eligible workspace projects and their native metadata, not from template declarations alone. Selection rejects unknown IDs and two managers with the same ecosystem. Bun and pnpm therefore cannot both claim npm packages in one workspace, while Go, Cargo, and Bun can coexist.

Generic native-file parsing lives in `core/bunx.go` and `core/cargox.go`. Built-in backends add Premise policy and convert native records to `PackageMetadata`. Bun package names can differ from template names; Cargo still checks its direct-package rules.

Bun and Cargo publication accumulate structural and task/registry preflight failures before mutation or publication. Any failure prevents the tag seam and all publication; after credentials or task configuration are corrected, rerun the release safely. Cargo backs up manifests and the lockfile, applies one version, regenerates the lockfile, publishes, and restores source files. Bun checks `NODE_AUTH_TOKEN` once per run and creates one temporary credential file per registry, reusing it for packages on that registry while isolating different registries.

Artifact fragments may contain only GoReleaser `builds` and `archives`. The release module merges them and rejects duplicate IDs or unsupported sections. Structural preflight runs before full publication preflight, and tagging occurs only after both pass.

Core cannot import built-in implementations because those implementations depend on core contracts. `cmd/package_manager_plugins.go` is therefore the composition root that wires built-ins into the registry.
