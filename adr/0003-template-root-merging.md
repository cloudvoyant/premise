# Focused template-root merge policies

- Status: accepted
- Decider: Siddharth K
- Date: 2026-09-20

## Context

A registry has shared files directly under `templates/` and template-specific files under `templates/<name>/`. Generation must combine those roots without replacing shared behavior or becoming a general configuration merger.

## Decision

Generation compares the substituted shared and selected trees and applies one of three focused tiers.

### Tier 1: root `mise.toml`

The root `mise.toml` is parsed as typed data. Tools retain Mise-specific semantic rules: compatible version selectors within one major version choose the greater version, incompatible major versions fail, and other scalar conflicts require an explicit choice. Environment conflicts can keep shared, use selected, rename the selected key with selected references updated, or abort.

Contract task collisions retain the selected task metadata and set its commands to shared first followed by selected. Root contract tasks are expected to be argument-free. A non-contract task collision keeps the shared task under its original name, copies the selected task under `<registry-prefix>:<task>`, and prints a user-visible notice.

### Tier 2: `.gitignore` and `.gitattributes`

These files use ordered line handling: shared lines come first and selected lines come second, with only adjacent duplicate lines removed. The order is intentional because later lines can override earlier Git policy.

### Tier 3: all other differing root files

Every other differing root file is resolved as a complete file: keep shared, use selected, or abort. Premise does not provide semantic merges for editor configuration, Prettier files, package manifests, or arbitrary ignore files.

## Simplifying assumption

Registries usually target different frameworks, so non-contract task and file collisions should be uncommon. Same-framework major dependency conflicts are likely genuine merge blockers and should fail rather than drive a general configuration merge engine.

## Implementation boundary

Generation resolves or pulls the registry first. `BuildGeneratePlan(GenerateParameters)` prepares the plan and decisions, and `ApplyGeneratePlan` performs dependency preflights, materialization, complete validation, and the destination rename.

## Consequences

The focused policies resolve collisions before destination creation, preserve ordered Git behavior, keep selected contract metadata, and make changed dependencies fail early. New smart formats require their own semantics and tests.

Crash-consistent transaction, lock, and journal machinery is out of scope.

## Links

- [Architecture](../docs/architecture.md)
- [User Guide](../docs/user-guide.md)
- [Mise configuration](https://mise.jdx.dev/configuration.html)
- [Git attributes](https://git-scm.com/docs/gitattributes)
- [Git ignore](https://git-scm.com/docs/gitignore)
