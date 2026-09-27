# Configuration Module

## Purpose

The configuration module owns the typed, validated representation of `premise.yaml`. It defines workspace identity, selected package managers, providers, generated projects, template registries, templates, and questionnaire fields.

## Design

### API

| API                                   | Use                                                                            |
| ------------------------------------- | ------------------------------------------------------------------------------ |
| `NewManifest(name)`                   | Create an in-memory manifest with default providers and an empty project list. |
| `LoadManifest(path)`                  | Decode and validate one `premise.yaml`.                                        |
| `SaveManifest(path, manifest)`        | Validate and atomically replace a manifest.                                    |
| `Config.Validate()`                   | Validate a manifest without filesystem writes.                                 |
| `Config.DeclaredTemplates()`          | Return template-registry declarations.                                         |
| `Config.FindTemplate(name)`           | Resolve one declared template by name.                                         |
| `Config.AddProject(project)`          | Add a generated-project record while enforcing unique names and paths.         |
| `FindManifest(start)`                 | Walk upward from a path to locate the owning manifest.                         |
| `InitializeWorkspace(root, template)` | Create a new monorepo manifest and root Mise configuration.                    |
| `InitializeTemplateRegistry(root)`    | Create a new template-registry manifest and templates directory.               |

### Usage

```go
manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
if err != nil {
    return err
}

manifest.Workspace.PackageManagers = []string{"go", "cargo"}
return core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest)
```

Package managers are selected explicitly:

```yaml
workspace:
  package_managers:
    - go
    - cargo
```

The list is ordered. Conflicts between managers in the same ecosystem are checked when registered backends are resolved for a release.

## Implementation Details

`LoadManifest` uses YAML known-field checking, rejects multiple YAML documents, normalizes nil project and template slices, and then calls `Config.Validate`. Unknown keys therefore fail instead of being silently ignored.

`Config.Validate` checks schema version, workspace kind, package-manager IDs, duplicate package-manager declarations, template names and paths, workspace-file patterns, questionnaire contracts, substitutions, and generated-project uniqueness. Validation is structural; backend registration and ecosystem conflicts are release-time concerns because configuration does not import implementations.

`SaveManifest` writes to a temporary file in the destination directory, syncs and closes it, then uses a same-directory rename. A failed write does not partially replace the existing manifest.

The configuration module does not inspect native package files, execute tasks, calculate versions, or publish releases. Those responsibilities belong to package-manager, task, version, and release modules.
