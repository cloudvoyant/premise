# Package Manager Module

## Purpose

The package-manager module defines how selected package ecosystems expose metadata, validate packages, contribute artifacts, and publish language packages. Core owns the contract and orchestration; `core/backends` contains built-in Go, Cargo, and Bun implementations.

## Design

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

### API

| API | Use |
| --- | --- |
| `PackageManagerBackend.ID()` | Return the value accepted by `workspace.package_managers`. |
| `PackageManagerBackend.Ecosystem()` | Return the incompatibility key, such as `npm`, `cargo`, or `go`. |
| `GetPackageMetadata(root, template)` | Parse one native package specification into `PackageMetadata`. |
| `ValidatePackage(root, template)` | Perform static package validation. |
| `WillPublishOk(ctx, root, template, version, task)` | Preflight one package without publication. |
| `PreflightPublication(ctx, root, version, task)` | Check workspace publication before changes. |
| `CreateGoReleaserConfig(root, manifest)` | Contribute GoReleaser `builds` and `archives`. |
| `SupportsPackages()` | Report whether the manager handles language packages. |
| `PublishPackages(ctx, root, version, task, ...)` | Preflight and publish eligible packages. |
| `ReleaseWorkspace(ctx, root, ...)` | Prepare the workspace used for artifact builds. |
| `RegisterPackageManagerBackend(backend)` | Register an implementation before invoking release APIs. |
| `PackageManagerGeneration` | Prepare shared dependency files and the validation workspace. |
| `PackageManagerReleaseTools` | Select tools for executable builds. |
| `PackageManagerRepositoryPublication` | Require source checks before the shared Git tag makes packages available. |
| `PackageMetadata` | Carry manager-neutral package name, version, path, registry, visibility, and publication capability. |

## Implementation Details

Premise monorepos contain packages that release together and services that depend on each other. Every package manager uses the same repository version and Git tag. Providers adapt native package files and commands to this shared release flow. They do not choose separate project versions.

The `workspace.package_managers` list selects the providers. A provider runs only when it appears in that list. Core calls their shared methods to check packages, build files, and run publication tasks. Native manifests still define package details when the package manager supports them.

Some providers need extra steps. The generation interface prepares root dependency files before a project is created. The release tools interface selects tools needed to build executables. The repository publication interface marks packages that become available through the Git tag itself. Core checks those packages before creating the tag. Registry uploads run in the later package publication job with their own credentials. Both jobs use the same release version.

### Go backend

Go packages share one root module. During generation, the backend combines template requirements and checksums with the root `go.mod` and `go.sum`. It removes the staged project manifests and copies existing package sources into the temporary validation workspace.

Go has no per-package publication or executable settings in `go.mod`. The backend reads these settings from each project's `package` block in `premise.yaml`. Template declarations can supply that block, and generation applies questionnaire substitutions before recording it with the project. Public projects run publication tasks. Private-local projects skip those tasks but remain in the root module download.

Before publication, the backend tests public projects from a committed Git archive with `GOWORK=off`. Ignored files cannot make an incomplete release pass. The backend rejects filesystem replacements and a module path that does not match the release major version. Git utilities export committed files. Go utilities parse native module files and inspect source package declarations. These utilities live in `core/extensions`. Workspace and release policy stays in `core/backends/go.go`.

The backend creates an archive for each declared Go application. A root main package keeps the existing CLI archive names. A workspace with only libraries creates a source release without executables. See [Using Premise with Go](../guides/go.md) for commands and import examples.
