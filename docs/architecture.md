# Architecture

## Overview

Premise is a Git-native platform engineering toolkit. It uses Git repositories, Mise, monorepo conventions, task contracts, templates, and CI workflows to make development and delivery consistent across projects.

Premise includes project generation today, but the whole product is not permanently create-only. Generation currently creates new projects without overwriting destinations; template migration and other lifecycle capabilities are planned as separate workflows.

## Requirements

- Manage development environments, including tool versions, environment values, and installation through `pm install` and Mise.
- Provide a lightweight, package-manager-forward monorepo model that does not replace the package manager's dependency and workspace responsibilities.
- Provide a task runner through `pm run`, layered over Mise tasks and monorepo conventions.
- Derive automatic CI from task contracts so projects expose stable lifecycle names instead of provider-specific task glue.
- Scaffold projects and generate them from local or remote templates.
- Support future template migration for existing projects without changing the create-only generation workflow.
- Establish future standardized infrastructure and secret management without making those planned systems appear implemented today.

## Design

The architecture has four cooperating components:

| Component                                                 | Responsibility                                                                                                                                                                                                                    |
| --------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Development Environment (`pm install` / Mise)**         | Mise owns declared tools, versions, environment resolution, and installation. Premise provides the workspace entry point and conventions around that environment.                                                                 |
| **Monorepo and Task Runner (`pm run` layered over Mise)** | Premise discovers the workspace and its projects, applies `apps/` and `libs/` conventions, and routes root or project task requests to Mise. Package managers remain responsible for package dependencies and workspace behavior. |
| **Scaffolding and Template Management**                   | `template_registry` declares selectable templates and explicit repository-root workspace files. A repository can also contain generated projects. Generation applies answers and creates one nested project.                      |
| **Automatic CI (GitHub Action and task contracts)**       | The GitHub Action installs the required environment and invokes `pm ci flow`. Contract task names give the CI flow a stable interface while each project owns the task implementation.                                            |

## Overall Module Dependency Tree

```text
[cmd]
  |
  +--> [Configuration] --> [Project]
  |
  +--> [Registry] --> [Template Source]
  |
  +--> [Questionnaire]
  |
  +--> [Template] -----------------+
  |                                |
  +--> [Generate] --> [Merge] -----+
  |                                |
  +--> [Development Environment] --+
  |                                |
  +--> [Task] ---------------------+--> [Mise]
  |                                |
  +--> [CI] -----------------------+
  |                                |
  +--> [Version] --> [Git + svu]   |
  |                                |
  +--> [Release] ------------------+
  |      `--> [PackageManagerPlugin] <--- [core/plugins]
  |
  `--> [Updater] --> [HTTP Installer]
```

## Command Dependency Graphs

Each map distinguishes modules called directly by the command from modules used internally by those entry-point modules. Indentation represents dependency ownership, not execution order.

### `pm init`

```text
[pm init]
  |
  `-- direct --> [Configuration]
                  initialize the selected workspace kind, manifest, and layout
```

### `pm install`

```text
[pm install]
  |
  +-- direct --> [Configuration]
  |               locate the workspace and inspect project Mise files
  |
  +-- direct --> [Development Environment]
  |               install tools declared by root and project Mise configuration
  |
  `-- direct --> [Task]
                  invoke the optional workspace-level install contract
```

### `pm run`

```text
[pm run <task|project:task>]
  |
  `-- direct --> [Task]
                  parse and route the task selector
                    |
                    +-- uses --> [Configuration]
                    |             resolve a project name to its recorded path
                    |
                    `-- uses --> [Mise boundary]
                                  execute the task in the resolved directory
```

### `pm generate`

```text
[pm generate [selector]]
  |
  +-- direct --> [Configuration]
  |               verify that the current directory is in a workspace
  |
  +-- direct --> [Registry]
  |               classify and resolve the selector argument
  |
  +-- direct --> [Questionnaire]
  |               provide interactive selector and answer collection
  |
  `-- direct --> [Generate]
                  own the complete generation operation
                    |
                    +-- uses --> [Configuration]
                    |             load and save the client manifest
                    |
                    +-- uses --> [Registry]
                    |             load the selected template declaration
                    |
                    +-- uses --> [Questionnaire]
                    |             collect answers declared by the template
                    |
                    +-- uses --> [Project]
                    |             validate and persist project provenance
                    |
                    +-- uses --> [Merge]
                    |             resolve registry-root file contributions
                    |
                    +-- uses --> [Template]
                    |             resolve, render, and validate template files
                    |
                    +-- uses --> [Task]
                    |             resolve the contract task set
                    |
                    `-- uses --> [Mise boundary]
                                  merge tool configuration and validate candidates
```

### `pm template init`

```text
[pm template init]
  |
  +-- direct --> [Configuration]
  |               locate the template-registry workspace
  |
  +-- direct --> [Questionnaire]
  |               select app or library kind when omitted
  |
  `-- direct --> [Template]
                  create the template declaration, files, and Mise contract
```

### `pm template detect`

```text
[pm template detect]
  |
  +-- direct --> [Configuration]
  |               locate the current Premise workspace
  |
  `-- direct --> [Registry]
                  classify its lifecycle from workspace configuration
```

### `pm template ls`

```text
[pm template ls]
  |
  +-- direct --> [Configuration]
  |               locate the current Premise workspace
  |
  `-- direct --> [Registry]
                  load and list declared template entries
```

### `pm template test`

```text
[pm template test]
  |
  +-- direct --> [Configuration]
  |               load the registry manifest
  |
  `-- direct --> [Template]
                  resolve and test every declared template
                    |
                    +-- uses --> [Task]
                    |             resolve each kind's contract task set
                    |
                    `-- uses --> [Mise boundary]
                                  execute the template contracts
```

### `pm ci flow`

```text
[pm ci flow <on-commit|on-merge|on-release>]
  |
  +-- direct --> [Configuration]
  |               locate the workspace root
  |
  +-- direct --> [Package Manager]
  |               register built-in manager implementations
  |
  `-- direct --> [CI]
                  select and gate lifecycle-specific behavior
                    |
                    +-- uses --> [Configuration / Registry]
                    |             detect workspace kind and declared templates
                    |
                    +-- uses --> [Mise boundary]
                    |             inspect and execute lifecycle tasks directly
                    |
                    `-- uses --> [Release]
                                  perform the permitted publication phase
```

### `pm version`

```text
[pm version <current|next|bump|rc>]
  |
  +-- direct --> [Configuration]
  |               locate the Premise repository root
  |
  `-- direct --> [Version]
                  derive stable, bumped, or release-candidate versions from Git
```

### `pm release`

```text
[pm release [plan|prepare|github|packages|snapshot]]
  |
  +-- direct --> [Configuration]
  |               locate the workspace and load release settings
  |
  +-- direct --> [Package Manager]
  |               register built-in manager implementations
  |
  `-- direct --> [Release]
                  own planning or the selected release operation
                    |
                    +-- uses --> [Version]
                    |             calculate and validate version and tag
                    |
                    +-- uses --> [Package Manager]
                    |             read metadata, preflight, prepare, and publish
                    |
                    `-- uses --> [Mise boundary]
                                  run RC tasks and resolve GoReleaser tooling
```

Release modes select subsets of the Release module's capabilities:

```text
plan      : version planning without mutation
prepare   : version planning and Git tag preparation
github    : artifact publication from an already prepared tag
packages  : language-package publication from an already prepared tag
snapshot  : artifact build without tag creation or publication
default   : all stable-release capabilities
```

### `pm update`

```text
[pm update [version]]
  |
  `-- direct --> [CLI-local updater]
                  validate the requested SemVer, download the bounded installer,
                  and replace the executable in its current installation directory
```

`pm update` intentionally has no dependency on a core domain module.

Dependencies point inward toward shared contracts. Core does not import built-in plugin implementations. The CLI registers implementations and then calls core workflows. `workspace.package_managers` selects plugins explicitly; native files provide package metadata but never select a manager. Managers that claim the same ecosystem, such as Bun and pnpm for npm packages, conflict.

Premise currently implements environment setup, monorepo task routing, scaffolding, template-root merging, and contract-driven CI. Template migration, standardized secret management, and standardized infrastructure are planned rather than implemented. Infrastructure providers and ownership boundaries remain future design work.

## Implementation

Mise owns tools, environments, and task execution. Premise manages monorepo conventions on top of Mise and uses task contracts for CI.

Generation relies on the expected monorepo structure when it selects a destination and records provenance. Template registry and generated-project capabilities may coexist; `workspace.kind` selects default CI behavior rather than enforcing exclusive repository contents.

Premise imposes conventions around secret management and artifact publishing. These conventions protect credentials and provide consistent release interfaces, but they do not yet constitute a complete standardized secret-management or infrastructure platform.

Infrastructure ownership is future work. Provider-specific infrastructure, provisioning, and long-term ownership rules must be defined by a later architecture and are not part of the current implementation.

The [Module Documentation](modules/README.md) index links one design document per major module, including API usage and implementation details. The detailed create-only generation and template-root merge design is documented in [Generation Architecture](generation.md). The focused merge policy remains in [ADR 0003](../adr/0003-template-root-merging.md).

## References

- [User Guide](user-guide.md)
- [Module Documentation](modules/README.md)
- [Configuration Module](modules/configuration.md)
- [Development Environment Module](modules/development-environment.md)
- [Project Module](modules/project.md)
- [Registry Module](modules/registry.md)
- [Questionnaire Module](modules/questionnaire.md)
- [Template Module](modules/template.md)
- [Generate Module](modules/generate.md)
- [Merge Module](modules/merge.md)
- [Task Module](modules/task.md)
- [Package Manager Module](modules/package-manager.md)
- [Version Module](modules/version.md)
- [Release Module](modules/release.md)
- [CI Module](modules/ci.md)
- [Generation Architecture](generation.md)
- [Infrastructure](infrastructure.md)
- [ADR 0003: Focused template-root merge policies](../adr/0003-template-root-merging.md)
- [Mise configuration](https://mise.jdx.dev/configuration.html)
- [GitHub Actions](https://docs.github.com/en/actions)
