# Core Modules

## Purpose

This document defines the main domain modules and their public boundaries. Package managers implement format-specific behavior. Core modules coordinate projects, tasks, versions, and releases without parsing package-manager files.

## Module map

| Module                 | Owns                                                                                  | Main API                                                                                       | Does not own                                               |
| ---------------------- | ------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| Configuration          | The validated `premise.yaml` model                                                    | `LoadManifest`, `SaveManifest`, `Config.Validate`                                              | Filesystem discovery or release execution                  |
| Project                | A generated project record and its template provenance                                | `Project`, `Config.AddProject`, `Config.FindTemplate`                                          | Package parsing or package publication                     |
| Template registry      | Template declarations and root-file merge policy                                      | `TemplateRegistry`, `Template`, registry and generation functions                              | Generated-project task execution                           |
| Task                   | Mise-backed root and project task execution                                           | `RunRootTask`, `RunProjectTask`, `ContractTasks`, `MiseTaskRunner`                             | Release version policy                                     |
| Package metadata       | Package-neutral facts parsed from native specifications                               | `PackageMetadata`, `ReadBunPackage`, `ReadCargoPackage`                                        | Whether or when a release runs                             |
| Package-manager plugin | Native package parsing, validation, artifact configuration, and publication mechanics | `PackageManagerPlugin`, `RegisterPackageManagerPlugin`                                         | Selecting itself from files or sequencing the full release |
| Version                | Semantic version calculation and release-candidate validation                         | `CurrentVersion`, `NextVersion`, `ReleaseCandidateVersion`                                     | Tags, artifacts, or package publication                    |
| Release                | Tag planning, selected-plugin orchestration, and credential boundaries                | `PlanStableRelease`, `PrepareStableRelease`, `PublishStableRelease`, `PublishLanguagePackages` | Native package-file parsing                                |
| CI                     | Lifecycle selection and release-phase gating                                          | `RunCIFlow`, `ParseCIFlow`, `ParseCIReleaseMode`                                               | Package-manager selection                                  |

## Project and package capabilities

`workspace.projects` records generated projects. Each record keeps its template reference, path, version, and generation answers. Task execution resolves the project path from this record and delegates to Mise.

A template registry can also contain publishable packages. During release planning, every enabled package-manager plugin reads each direct template package and returns `PackageMetadata`. This metadata contains the package name, version, path, manager, ecosystem, registry, visibility, and publication capability. Missing native package files mean that the plugin does not handle that template.

Package managers are selected explicitly at the workspace level:

```yaml
workspace:
  package_managers:
    - go
    - cargo
    - bun
```

The list order is release order. Two selected plugins cannot claim the same ecosystem. For example, Bun and pnpm both claim the `npm` ecosystem and therefore cannot be enabled together. Go, Cargo, and Bun can coexist because they claim different ecosystems.

## Plugin API

A plugin provides these capabilities:

- `ID` identifies the value used in `workspace.package_managers`.
- `Ecosystem` identifies incompatible managers that operate on the same package format.
- `GetPackageMetadata` parses one template package into the shared metadata shape.
- `ValidatePackage` performs static package validation.
- `WillPublishOk` checks one eligible package without publishing it.
- `CreateGoReleaserConfig` contributes downloadable artifact definitions.
- `PublishPackages` performs package-manager-specific publication after preflight.
- `ReleaseWorkspace` prepares the artifact-build workspace.

Core never imports plugin implementations. The CLI is the composition root and registers built-ins explicitly. Library clients can register custom implementations before invoking release APIs.

## Release sequence

1. Load and validate `premise.yaml`.
2. Resolve `workspace.package_managers` against registered plugins.
3. Reject unknown IDs and conflicting ecosystems.
4. Parse package metadata and collect validation failures before mutation or publication.
5. Create or reuse one repository release version.
6. Combine downloadable artifact definitions from selected plugins.
7. Publish GitHub artifacts.
8. Publish eligible language packages with scoped credentials.

A context passed into task, registry, and release operations carries cancellation and deadlines only. It does not contain project state or credentials.
