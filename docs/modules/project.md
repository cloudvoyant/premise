# Project Module

## Purpose

The project module defines the persisted identity and provenance of a generated project. A project record stores its name, template selector, template version, workspace-relative path, and generation answers.

## Design

### API

| API                                             | Use                                                                                 |
| ----------------------------------------------- | ----------------------------------------------------------------------------------- |
| `Project`                                       | Represent one entry in `workspace.projects`.                                        |
| `ValidateProjectName(name)`                     | Validate a project identifier.                                                      |
| `Config.AddProject(project)`                    | Register a project while enforcing unique names and paths.                          |
| `KindDirectory(kind)`                           | Map `app` or `lib` to its conventional workspace directory.                         |
| `RunProjectTask(ctx, root, project, task, ...)` | Resolve a registered project and execute one of its Mise tasks.                     |
| `HasProjectMiseConfigs(root)`                   | Report whether conventional project roots contain project-level Mise configuration. |

### Usage

```go
project := core.Project{
    Name:     "billing",
    Template: "cloudvoyant/premise:premise-app",
    Version:  "0.1.0",
    Path:     "apps/billing",
    Answers:  map[string]string{"name": "billing"},
}
if err := manifest.AddProject(project); err != nil {
    return err
}
```

A project task is addressed by the persisted project name rather than by an arbitrary directory:

```go
err := core.RunProjectTask(ctx, root, "billing", "test", stdin, stdout, stderr)
```

## Implementation Details

`Project` is part of the validated configuration model. `Config.Validate` requires a valid name, a template selector, and a normalized relative path inside the workspace. Project names and paths must be unique.

`Config.AddProject` checks collisions, appends the record, sorts projects by path for deterministic YAML, and revalidates the complete manifest.

The project module records template provenance but does not resolve registries, materialize files, merge root configuration, or validate generated candidates. Those operations belong to the [Generate Module](generate.md). It also does not store a duplicate task list: tasks remain defined by the project’s Mise configuration and are resolved by the [Task Module](task.md).
