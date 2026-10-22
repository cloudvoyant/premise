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
before schema migrations. Deployment follows the update and required migrations. Infra and migration failures
stop the flow. Application deploy or health failures call client rollback tasks, which
use platform history. Do not add custom recovery storage, ledgers, or locks. Changes to this flow require explicit user approval. Run live
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

Keep Cobra flag parsing inside each command's RunE body. Execution helpers take
plain inputs and do not accept Cobra commands. Read CI release environment variables
inside the release module and expose the result as a typed struct; command files
must not know those variable names.

# Docs

Use soft wraps for documentation prose: keep each paragraph on one source line.
Prettier enforces this for `docs/**/*.md` through `proseWrap: never`.

Put Usage before API sections. Use short sentences, working examples, and diagrams
for flows. Keep each module focused on its own commands and responsibilities.

# Tests

Prioritize command end-to-end tests, then module integration tests. Avoid unit
tests that only mirror implementation details. Keep reusable files and scripts
in test fixtures. Organize tests into Arrange, Act, and Assert sections with
blank lines between them. Name each test for both its behavior and its expected
outcome.

Organize E2E coverage under Scaffold, Infra, Secrets, and CI suites. Use nested
`t.Run` contexts named `when ...` and outcome tests named `it ...`. Keep scenario
actions and assertions visible in the suite; move reusable setup and process
execution into helpers. Each context must work when selected with `go test -run`;
do not make one sibling scenario depend on another sibling having run. Test
cleanup behavior explicitly, and retain `t.Cleanup` as the resource safety net.

# Module organization

Keep infra querying and setup in the infra module, and application deployment and rollback tasks
in the deploy module. Put provider interfaces and implementations in core/providers.
Put newly introduced external-tool wrappers in core/extensions. Move existing wrappers
in a separate follow-up. Avoid comments that repeat a command's name and description.
