# Focused template-root merge policies

- Status: accepted
- Deciders: Siddharth K
- Date: 2026-09-20

Technical Story: [PR #10](https://github.com/cloudvoyant/premise/pull/10)

## Context and Problem Statement

Template registries contain shared files directly under `templates/` and selected-template files under `templates/<name>/`. Generation must combine those roots without silently discarding shared behavior, while avoiding a general-purpose configuration merge system whose semantics would be difficult to predict and maintain.

## Decision Drivers

- Preserve the meaning of formats for which focused merge rules are well-defined.
- Make collisions explicit and safe before anything is published at the destination.
- Preserve ordered Git policy and the selected template's contract metadata.
- Detect changed selected dependency values before generation changes the client repository.
- Keep the implementation understandable, testable, and limited to the template-root use case.

## Considered Options

- Focused three-tier root merge: typed `mise.toml` rules, ordered `.gitignore` and `.gitattributes` handling, and complete-file decisions for other differences.
- Selected-template-wins overlay for every collision.
- Complete-file choice for every differing file.
- General semantic merge framework for arbitrary configuration.

## Decision Outcome

Chosen option: "Focused three-tier root merge," because it provides useful semantics for the formats the registry contract understands, exposes genuinely unresolved choices to the user, and avoids pretending that arbitrary configuration files have safe universal merge rules.

Generation first resolves and substitutes the shared and selected roots, then applies these tiers. For typed root `mise.toml` data, Mise-specific rules apply: compatible tool version selectors within one major version choose the greater version, incompatible major versions fail, and other scalar conflicts require an explicit choice. Environment conflicts may keep the shared value, use the selected value, rename the selected key with selected references updated, or abort.

Contract task collisions retain the selected task metadata and run shared commands first followed by selected commands. Root contract tasks are expected to be argument-free. A non-contract task collision keeps the shared task under its original name, namespaces the selected task as `<registry-prefix>:<task>`, and reports that choice to the user.

For `.gitignore` and `.gitattributes`, shared lines are emitted first and selected lines second, with only adjacent duplicate lines removed. For ordinary differing files, the complete file is kept from shared, taken from selected, or rejected through an interactive conflict decision; no partial file merge is attempted.

Before destination publication, changed selected dependency values are preflighted in disposable copies. The complete generated candidate is validated before it is renamed into the destination. Generation is create-only and does not provide update or migration support for existing projects.

### Positive Consequences

- Typed `mise.toml` conflicts retain relevant Mise semantics instead of being treated as opaque text.
- Ordered Git policy remains meaningful because shared rules precede selected rules and adjacent duplicates are removed without reordering other lines.
- Complete-file decisions make ordinary collisions explicit, while selected contract metadata and shared-then-selected commands preserve the contract behavior.
- Dependency preflight and complete candidate validation happen before destination publication, reducing the chance of leaving a partial generated project.
- The policy is narrow enough to add focused tests and new format-specific rules deliberately.

### Negative Consequences

- The implementation contains format-specific code for `mise.toml`, `.gitignore`, and `.gitattributes` rather than one uniform merger.
- Unresolved ordinary differences require interactive decisions, and an aborted decision prevents generation.
- Generation is create-only, so there is no update or migration support for projects generated previously.
- Formats outside the supported rules are not semantically merged; editor configuration, package manifests, Prettier files, and arbitrary ignore files use complete-file choices.
- The selected namespace for non-contract task collisions and the dependency preflight add user-visible behavior and processing before publication.

## Pros and Cons of the Options

### Focused three-tier root merge

This option applies narrowly defined semantics to typed `mise.toml`, ordered Git policy files, and complete-file resolution elsewhere.

- Good, because it preserves meaning where the project has explicit knowledge of the format.
- Good, because it preserves shared behavior, selected contract metadata, and the intentional order of Git rules.
- Good, because it makes ordinary unresolved differences visible instead of silently choosing one side.
- Good, because it can validate dependencies and the complete candidate before publication.
- Bad, because format-specific code must be maintained and extended deliberately.
- Bad, because users must make interactive decisions for unresolved ordinary differences.
- Bad, because it does not support updates or migrations and does not semantically merge unsupported formats.

### Selected-template-wins overlay

This option would copy the selected template over shared content whenever both roots contain a collision.

- Good, because the rule is simple to explain and implementation is comparatively small.
- Good, because selected-template authors can control the resulting colliding content directly.
- Bad, because shared behavior can be silently discarded.
- Bad, because it cannot preserve shared-first contract commands or ordered Git policy without adding exceptions.
- Bad, because it hides unresolved ordinary differences rather than asking for a decision.

### Complete-file choice for every differing file

This option would treat every differing file as an opaque whole and choose shared, selected, or abort.

- Good, because it is predictable and avoids unsafe text or configuration merges.
- Good, because it works consistently for formats the project does not understand.
- Bad, because it loses useful typed `mise.toml` semantics, including task, tool, and environment handling.
- Bad, because it cannot express shared-first and selected-second ordered Git policy without treating those files specially.
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
