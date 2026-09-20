# Generation Architecture

## Overview

Premise's current generation workflow is create-only: it resolves one template from a local or remote registry, creates a new project, and records its provenance in the workspace manifest.

Generation does not overwrite an existing destination or update an existing project. Template migration is a separate future workflow, not an implicit extension of this design.

## Requirements

- Resolve a selector to a registry and a declared template, whether the source is local or remote.
- Apply questionnaire answers and literal substitutions consistently to the selected template and shared registry files.
- Make root collisions visible before materialization and give each supported collision a deterministic decision.
- Never overwrite an existing destination.
- Run dependency preflight and final validation before publishing the generated project.
- Persist template provenance, including the selector, project path, answers, and declared version when available.
- Remove a newly published destination when final manifest registration or persistence fails.

## Design

### Registry structure

A registry has a manifest and a `templates/` directory. Regular files placed directly under `templates/` are shared files. The selected template is the tree at `templates/<name>/`; sibling template directories are not shared content. Shared files and the selected tree are compared after substitutions are applied.

Selector resolution first identifies the registry source and then the template declaration. Local selectors can point at a registry on disk. Remote selectors identify a repository and template, and the resolved source is fetched or refreshed through the registry cache. `ResolveTemplateSource` returns the source root and the selected `TemplateSelection` used by generation.

### Three-tier generation

The merge has three focused tiers:

1. **Tier 1: root `mise.toml`.** Typed Mise data is merged with Mise-aware rules. Compatible tool versions can select the greater version within one major version; incompatible major versions fail. Environment and other scalar conflicts require a decision. Contract task collisions retain selected metadata and append shared commands before selected commands. Non-contract task collisions retain the shared name and namespace the selected task as `<registry-prefix>:<task>`, with a notice.
2. **Tier 2: `.gitignore` and `.gitattributes`.** Files are normalized into ordered lines, then shared lines are followed by selected lines and only adjacent duplicates are removed.
3. **Tier 3: other differing root files.** The complete shared file, the complete selected file, or an abort is chosen. Premise does not attempt general semantic merges for arbitrary configuration files.

One-sided files are copied without a conflict decision. Equal files coalesce without a decision. Root collisions are displayed in path order before the destination is created.

```mermaid
flowchart LR
  S[Selector] --> R[ResolveTemplateSource]
  R --> Q[Answers and substitutions]
  Q --> P[BuildMergePlan(TemplateGeneration)]
  P --> D[Preview and decisions]
  D --> X[ExecuteMergePlan]
  X --> V[Preflight and final validation]
  V --> N[Rename new destination]
  N --> M[Persist provenance manifest]
```

## Implementation

`ResolveTemplateSource` resolves local or remote registries and returns the selected source and template identity. Generation then loads the template declaration, asks its questions, validates the project name, and resolves literal substitutions.

`BuildMergePlan(TemplateGeneration)` validates the client repository root, project path, source directories, and absent destination. It stages the substituted selected tree, reads direct shared files from the registry `templates/` root, builds the comparison entries, resolves the three merge tiers, and keeps the prepared plan private.

`ExecuteMergePlan` runs a dependency preflight for each selected-template tool change in a disposable copy. It then materializes the merge in a private stage, copies that stage for final validation, runs `mise install` and every required template contract, and renames the validated stage into the destination. The rename is the publication boundary; an existing destination is rejected.

`core/misex.go` provides the Mise process boundary and typed `mise.toml` operations. It extracts and merges tools, environment values, root values, and tasks. Contract task commands append shared then selected commands, while non-contract collisions use the registry-prefixed namespace.

After `ExecuteMergePlan` succeeds, generation adds a `Project` to `workspace.projects` and saves the final `premise.yaml`. If project registration or manifest persistence fails, it removes the newly created destination. Temporary selected, preflight, and validation directories are cleaned up by the merge plan. Crash-consistent recovery across the destination and manifest is outside this workflow.

Migration of an existing project from one template version to another is a separate future workflow. It must define its own provenance, conflict, validation, and rollback behavior rather than silently changing this create-only path.

## References

- [Overall Premise Architecture](architecture.md)
- [User Guide](user-guide.md)
- [ADR 0003: Focused template-root merge policies](../adr/0003-template-root-merging.md)
- [Mise configuration](https://mise.jdx.dev/configuration.html)
- [Git attributes](https://git-scm.com/docs/gitattributes)
- [Git ignore](https://git-scm.com/docs/gitignore)
