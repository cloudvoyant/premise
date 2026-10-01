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

## Maintainer notes

The package manager list in `premise.yaml` is the source of release policy. A manager runs only when it is listed. File presence does not select a manager.

The module keeps package-manager contracts separate from built-in backends. This lets library users register their own backends and keeps core independent of the built-in implementations.

Before publication, the release process checks manager selection, artifact configuration, tasks, and credentials. If a check fails, no tag or package is published. Fix the cause and run the release again.
