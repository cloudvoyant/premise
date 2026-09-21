# Focused template-root merge policies

- Status: accepted
- Deciders: Siddharth K
- Date: 2026-09-20

Technical Story: [PR #10](https://github.com/cloudvoyant/premise/pull/10)

## Context and Problem Statement

Template registries contain client-root files directly under `templates/` and selected-project files under `templates/<name>/`. Generation must merge the direct files into the client workspace root and copy the selected tree to `apps/<name>` or `libs/<name>` without conflating those two target locations.

## Decision Drivers

- Preserve the meaning of formats for which focused merge rules are well-defined.
- Make collisions explicit and safe before anything is published at the destination.
- Preserve ordered root Git policy and the existing client's root task metadata.
- Detect changed client-root tool values before generation changes the workspace.
- Keep the selected project independent from workspace-root conflict handling.
- Keep the implementation understandable, testable, and limited to the template-root use case.

## Considered Options

- Focused three-tier root merge: typed `mise.toml` rules, ordered `.gitignore` and `.gitattributes` handling, and complete-file decisions for other differences.
- Registry-root-wins overlay for every collision.
- Complete-file choice for every differing file.
- General semantic merge framework for arbitrary configuration.

## Decision Outcome

Chosen option: "Focused three-tier root merge," because it provides useful semantics for the formats the registry contract understands, exposes genuinely unresolved choices to the user, and avoids pretending that arbitrary configuration files have safe universal merge rules.

Generation first substitutes the direct registry files and selected template independently. It compares direct files only with matching paths in the client workspace root. The selected template is staged only at its final project path and never participates in root-file conflicts. For typed root `mise.toml` data, Mise-specific rules apply: compatible tool version selectors within one major version choose the greater version, incompatible major versions fail, and other scalar conflicts require an explicit choice. Environment conflicts may keep the registry value, use the client value, rename the client key with client references updated, or abort.

Root contract task collisions retain the existing client task metadata and run registry commands first followed by client commands. Root contract tasks are expected to be argument-free. A non-contract collision keeps the registry task under its original name, namespaces the client task as `<registry-prefix>:<task>`, and reports that choice to the user.

For `.gitignore` and `.gitattributes`, registry lines are emitted first and existing client lines second, with only adjacent duplicate lines removed. For ordinary differing root files, the complete registry file is kept, the complete client file is retained, or generation is aborted; no partial file merge is attempted.

Before publication, changed client-root tool values are preflighted in disposable workspace candidates. The complete candidate contains the planned workspace-root files and the selected project at its final relative path. Generation validates this layout before publishing root changes and renaming the project destination. Generation is create-only and does not provide existing-project migration.

### Positive Consequences

- Typed `mise.toml` conflicts retain relevant Mise semantics instead of being treated as opaque text.
- Ordered Git policy remains meaningful because registry rules precede existing client rules and adjacent duplicates are removed without reordering other lines.
- Complete-file decisions make ordinary client-root collisions explicit, while client task metadata and registry-then-client commands preserve root contract behavior.
- Selected-template files remain isolated in their project destination and cannot collide with workspace-root files.
- Dependency preflight and complete candidate validation happen before destination publication, reducing the chance of leaving a partial generated project.
- The policy is narrow enough to add focused tests and new format-specific rules deliberately.

### Negative Consequences

- The implementation contains format-specific code for `mise.toml`, `.gitignore`, and `.gitattributes` rather than one uniform merger.
- Unresolved ordinary client-root differences require interactive decisions, and an aborted decision prevents generation.
- Generation is create-only, so there is no update or migration support for projects generated previously.
- Formats outside the supported rules are not semantically merged; editor configuration, package manifests, Prettier files, and arbitrary ignore files use complete-file choices.
- The client-task namespace for non-contract collisions and the dependency preflight add user-visible behavior and processing before publication.

## Pros and Cons of the Options

### Focused three-tier root merge

This option applies narrowly defined semantics to typed `mise.toml`, ordered Git policy files, and complete-file resolution elsewhere.

- Good, because it preserves meaning where the project has explicit knowledge of the format.
- Good, because it preserves registry behavior, existing client task metadata, and the intentional order of root Git rules.
- Good, because it makes ordinary unresolved differences visible instead of silently choosing one side.
- Good, because it can validate dependencies and the complete candidate before publication.
- Bad, because format-specific code must be maintained and extended deliberately.
- Bad, because users must make interactive decisions for unresolved ordinary differences.
- Bad, because it does not support updates or migrations and does not semantically merge unsupported formats.

### Registry-root-wins overlay

This option would copy registry root files over existing client-root content without explicit root-file policies.

- Good, because the rule is simple to explain and implementation is comparatively small.
- Good, because registry authors can control the resulting root content directly.
- Bad, because existing client behavior can be silently discarded.
- Bad, because it cannot preserve registry-first contract commands or ordered root Git policy without adding exceptions.
- Bad, because it hides unresolved ordinary differences rather than asking for a decision.

### Complete-file choice for every differing file

This option would treat every differing root file as an opaque whole and choose the registry file, the existing client file, or abort.

- Good, because it is predictable and avoids unsafe text or configuration merges.
- Good, because it works consistently for formats the project does not understand.
- Bad, because it loses useful typed `mise.toml` semantics, including task, tool, and environment handling.
- Bad, because it cannot express registry-first and client-second ordered Git policy without treating those files specially.
- Bad, because choosing a complete file can discard useful content from the other root even when a safe focused merge is known.

### General semantic merge framework for arbitrary configuration

This option would provide extensible, format-aware merging for arbitrary configuration files and potentially many future formats.

- Good, because it could preserve more content across a wide range of structured formats.
- Good, because future formats could share framework machinery for parsing, conflict reporting, and policy selection.
- Bad, because arbitrary configuration formats do not share reliable merge semantics, so false confidence and surprising output would be likely.
- Bad, because the framework would be substantially more complex than the template-root problem requires.
- Bad, because it would expand the testing and maintenance surface and still require format-specific policies.
- Bad, because it would not justify update or migration support merely by making merges more general.

## Links

- [Architecture](../docs/architecture.md)
- [User Guide](../docs/user-guide.md)
- [Mise configuration](https://mise.jdx.dev/configuration.html)
- [Git attributes](https://git-scm.com/docs/gitattributes)
- [Git ignore](https://git-scm.com/docs/gitignore)
