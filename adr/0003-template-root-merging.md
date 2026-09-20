# Merge template roots with focused policies

- Status: accepted
- Decider: Siddharth K
- Date: 2026-09-20

## Context and Problem Statement

A registry can place shared files directly under `templates/` and template-specific files under `templates/<name>/`. The old copy-then-overlay flow replaced shared root files without showing the conflict. This was unsafe for ordered Git policy files and for `mise.toml`, where both sides can contain required behavior.

How can generation combine these roots without becoming a general configuration merger or a transaction system?

## Decision Drivers

- Show every root collision before writing the destination.
- Preserve ordering and override semantics in Git policy files.
- Treat Mise tools, tasks, and environment values as typed data.
- Test dependency changes separately from the complete candidate.
- Keep the current create-only stage, rename, and manifest-save flow.
- Avoid new dependencies and infrastructure.

## Considered Options

- Keep selected-template overwrite behavior.
- Apply a generic text or TOML merge to every collision.
- Add focused handlers for known files and use a complete-file choice for all other collisions.
- Add a lock, journal, and multi-resource transaction around generation.

## Decision Outcome

Choose focused handlers for `.gitignore`, `.gitattributes`, and `mise.toml`. Use a complete-file decision for every other differing root file. Keep the existing simple persistence model.

Premise builds one path-sorted comparison after substitutions. Ordered Git files keep shared lines first and selected lines second. The Mise handler extracts each input independently, merges typed sections, and preserves unknown root keys through recursive rules.

When a merged tool version changes an existing selected-template tool, Premise tests that one change in a disposable copy. It then materializes the full stage and tests another disposable copy. Only a validated stage can be renamed to the destination.

### Positive Consequences

- You see conflicts and their strategies before generation changes the workspace.
- Git ignore negations and attribute overrides keep their selected-side precedence.
- A failed tool upgrade names the dependency that failed.
- Complete-candidate checks catch interactions among merged files.
- Template authors keep the current manifest and directory model.

### Negative Consequences

- Canonical Mise output does not preserve source comments or formatting.
- Ordinary collisions require an interactive complete-file decision.
- The destination rename and manifest save are not one crash-consistent transaction.
- New smart formats require separate semantics and tests.

## Deliberate limits

Premise does not smart-merge `.editorconfig`, Prettier files, package manifests, or arbitrary ignore files. It does not update existing generated projects. It does not add locks, journals, recovery state, transaction directories, platform-specific atomic wrappers, or schema changes.

## Links

- [Architecture](../docs/architecture.md)
- [User Guide](../docs/user-guide.md)
- [Mise configuration](https://mise.jdx.dev/configuration.html)
- [Git attributes](https://git-scm.com/docs/gitattributes)
- [Git ignore](https://git-scm.com/docs/gitignore)
