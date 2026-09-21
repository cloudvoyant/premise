# Generation Architecture

## Overview

Premise generation is create-only. It resolves one template, expands the registry's declared root-file patterns, merges those files into the client workspace root, creates one project under `apps/<name>` or `libs/<name>`, validates the resulting workspace candidate, and records the project's provenance.

Generation does not overwrite an existing project destination or update an existing project. Template migration is a separate future workflow.

## Requirements

- Resolve a selector to a registry and one declared template, whether the source is local or remote.
- Read client workspace files only from `template_registry.workspace_files` in the registry manifest.
- Restrict workspace-file patterns to direct regular files at the registry repository root.
- Always exclude `premise.yaml`, directories, symlinks, special files, and nested paths.
- Apply questionnaire answers and literal substitutions to declared registry files and selected-template files.
- Merge declared registry files against files at the client workspace root.
- Copy the selected `templates/<name>` tree only to its new `apps/<name>` or `libs/<name>` destination.
- Never treat selected-template files as conflicts with client-root files.
- Validate changed root tool versions independently and validate the complete workspace candidate before publication.
- Restore changed workspace-root files and remove the new project if final project registration or manifest persistence fails.

## Registry configuration

Template configuration is grouped under `template_registry`:

```yaml
template_registry:
  workspace_files:
    - .gitignore
    - package.json
    - "*.config.js"
  templates:
    - name: premise-app
      kind: app
      path: templates/premise-app
      questions:
        - prompt: "App name:"
          type: string
          populate: name
```

`workspace_files` is required when `template_registry` exists. An explicit empty list means the registry shares no workspace files. Every pattern is relative to the repository root, cannot contain a path separator, and must match at least one eligible direct regular file. Matches are deduplicated and sorted. `premise.yaml` is registry metadata and is never copied, even when a glob would otherwise match it.

Every template declaration has an explicit repository-relative `path`. Existing registries can keep paths such as `templates/premise-app`; the path is independent of `workspace_files`. Direct files inside the physical `templates/` directory have no generation meaning unless a template path contains them.

A repository can contain both `workspace.projects` and `template_registry`. `workspace.kind` continues to select the default CI lifecycle, but it no longer forbids the other capability. Template commands check for `template_registry` rather than rejecting monorepo workspaces.

### Three-tier workspace-root merge

Only a declared registry file that meets an existing client-root path needs collision handling:

1. **Tier 1: root `mise.toml`.** The registry's Mise configuration merges with the existing client workspace Mise configuration. Compatible tool versions can select the greater version within one major version; incompatible major versions fail. Environment and other scalar conflicts require a decision. Contract task collisions retain client metadata and append registry commands before client commands. Non-contract task collisions retain the registry task name and namespace the client task as `<registry-prefix>:<task>`.
2. **Tier 2: root `.gitignore` and `.gitattributes`.** Registry lines are followed by client lines, and only adjacent duplicates are removed.
3. **Tier 3: other differing root files.** The complete registry file, complete existing client file, or an abort is chosen. Premise does not attempt semantic merging for arbitrary configuration files.

A declared file whose client-root path does not exist is copied without a decision. Equal files coalesce. Files in the selected template never enter this comparison.

```mermaid
flowchart LR
  S[Selector] --> R[Resolve registry and template]
  R --> F[Expand declared root files]
  F --> Q[Answers and substitutions]
  Q --> P[Plan client-root changes]
  P --> T[Stage selected project]
  T --> V[Validate workspace candidate]
  V --> W[Publish root files]
  W --> N[Rename project destination]
  N --> M[Persist provenance manifest]
```

## Implementation

`BuildGeneratePlan(GenerateParameters)` validates the client workspace, absent project destination, registry root, and selected template. It stages the substituted selected tree independently. It expands the manifest's workspace-file patterns against direct registry-root files, compares only those paths against the client workspace root, and resolves the three merge tiers.

`ApplyGeneratePlan` materializes the selected project in a private stage. For validation, it creates a disposable workspace candidate containing the client root files, planned root-file outputs, and the selected project at its final relative path. It validates the candidate root Mise installation, runs root contracts when the root defines the complete contract, and runs the selected project's complete contract. Successful command output remains hidden; the first failed command's diagnostics are shown.

After validation, Premise writes the planned root files and renames the staged project into its destination. It keeps in-memory copies of changed root files until the workspace manifest is saved. A publication or manifest error restores those files and removes the new destination. There is no lock, journal, crash-recovery protocol, platform-specific transaction wrapper, or existing-project migration.

`core/config.go` owns manifest validation and workspace-file pattern rules. `core/misex.go` owns typed Mise extraction, encoding, mutation, and merge behavior. `core/merge.go` owns file-level decisions. `core/generate.go` owns pattern expansion, planning, staging, candidate validation, root publication, and destination publication.

## References

- [Overall Premise Architecture](architecture.md)
- [User Guide](user-guide.md)
- [ADR 0003: Focused template-root merge policies](../adr/0003-template-root-merging.md)
- [Mise configuration](https://mise.jdx.dev/configuration.html)
- [Git attributes](https://git-scm.com/docs/gitattributes)
- [Git ignore](https://git-scm.com/docs/gitignore)
