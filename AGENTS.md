# Go file organization

Follow the section labels used in `core/template.go`:

- Put exported functions and methods under `// Public API -----------------------------------------------------------------`.
- Put unexported functions, helper types, and methods on unexported types at the bottom of the file under `// Private helpers ------------------------------------------------------------`.
- Keep each declaration's documentation with that declaration.

# GitHub Actions

This repository and generated Premise projects may contain only `on-commit.yml`,
`on-deploy.yml`, and `on-merge.yml` workflows. Do not add reusable or test workflows.
Keep plan, task fan-out, and publication before the environment flow. Existing
environments migrate before the infrastructure update; new environments migrate
after the first apply. Clients apply database infrastructure changes separately
before schema migrations. Deployment and recovery follow the update and required
migrations. Changes to this flow require explicit user approval. Run live
infrastructure validation separately on demand; do not add it to CI.

Infrastructure authentication belongs to Premise provider adapters, invoked with
`pm infra auth --ci` at the relevant pipeline steps. Do not use cloud-provider
GitHub Actions for authentication or tool setup. Install tools through Mise.
Keep provider logic independent of the CI runner; put runner-specific identity
exchange and output formatting in CI adapters. Unsupported providers use the
root `mise.toml` `ci:auth` fallback after native authentication. Pulumi reuses
the provider credentials or CI secrets.

Use released version tags for actions instead of long commit hashes where a
compatible release exists. Keep generated workflows in `assets/github-workflows`
consistent with the repository workflows. A release candidate tag is acceptable
for a tandem PR that needs an interface absent from the stable release.

Keep `on-deploy.yml` manual-only (`workflow_dispatch`). Feature-push previews belong
in `on-commit.yml`. Preview cleanup belongs in `on-merge.yml`, including the
branch-deletion fallback.

# Cobra commands

Define each subcommand with a named function such as `InfraCommand() *cobra.Command`
under `Public API`. Do not prefix these functions with `New`. Create the command,
configure its flags, attach its children, and return it in that function.
Keep flag values local to each command instance.
Keep one `init` in `cmd/root.go` that only registers top-level commands explicitly.
Put execution helpers under `Private helpers`. Do not build commands in a loop.

# Docs

Put Usage before API sections. Use short sentences, working examples, and diagrams
for flows. Keep each module focused on its own commands and responsibilities.

# Tests

Prioritize command end-to-end tests, then module integration tests. Avoid unit
tests that only mirror implementation details. Keep reusable files and scripts
in test fixtures. Organize tests into Arrange, Act, and Assert sections with
blank lines between them. Name each test for both its behavior and its expected
outcome.
