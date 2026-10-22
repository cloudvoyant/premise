# Hard rules

- Keep only `on-commit.yml`, `on-deploy.yml`, and `on-merge.yml`. Do not add reusable or test workflows.
- Keep checks, task fan-out, and publication before environment deployment.
- Existing environments migrate before infra updates. New environments migrate after the first apply. Clients apply database prerequisites separately and keep migrations backward compatible.
- Infra or migration failure stops the flow. App deploy or health failure runs client rollback tasks using platform history. Do not add recovery stores, artifact ledgers, or custom locks.
- Keep `on-deploy` manual-only. Feature-push previews run on commit. Preview cleanup runs on merge, with a branch-deletion fallback.
- Get explicit user approval before changing this lifecycle. See [CI](docs/guides/ci.md) and [migrations](docs/guides/migrations.md).
- Run live infra validation separately on demand. Do not add it to CI.
- Authenticate through `pm infra auth --ci` and provider adapters. Do not use cloud-provider Actions for auth or tool setup. Install tools through Mise.
- Unsupported providers use the root `ci:auth` task after native auth. Pulumi reuses provider credentials or protected CI secrets. See [providers](docs/providers/README.md).
- Use released action tags. A compatible RC is allowed for a tandem PR. Keep generated workflows consistent with repository workflows.

# Organization

- Keep infra setup and querying in `core/infra.go`, app deploy and rollback in `core/deploy.go`, and CI sequencing in `core/ci.go`.
- Put provider interfaces and implementations in `core/providers`. Keep provider behavior independent of the CI runner; put runner identity exchange and output formatting in CI adapters.
- Put new external-tool wrappers in `core/extensions`. Move existing wrappers in a separate follow-up.
- Define application tasks in `mise.toml`. Do not add standalone task dispatcher scripts.
- Keep reusable test files in fixtures. Process doubles may replace tools for module failure tests.
- See [module docs](docs/modules/README.md) and the [E2E guide](e2e/README.md).

# Style

## Go

- Follow `core/template.go`. Exported functions and methods go under `// Public API -----------------------------------------------------------------`.
- Put unexported functions, helper types, and methods on unexported types at the bottom under `// Private helpers ------------------------------------------------------------`.
- Keep documentation with each declaration. Avoid comments that repeat its name and description.

## Cobra

- Define each command in a named function such as `InfraCommand() *cobra.Command`. Do not use a `New` prefix or build commands in loops.
- Create the command, flags, and children in that function. Keep flag values local to each command instance.
- Keep one `init` in `cmd/root.go`; it only registers top-level commands explicitly.
- Parse flags inside `RunE`. Execution helpers accept plain inputs, not Cobra commands.
- Read release environment variables inside the release module and expose typed results.

## Documentation

- Use short sentences, working examples, and diagrams for flows. Put Usage before API.
- Keep each paragraph on one source line. Prettier enforces soft wraps in `docs/**/*.md`.
- Keep module docs focused on their own commands and responsibilities.

## Tests

- Prefer command E2E tests, then module integration tests. Avoid tests that mirror implementation.
- Separate Arrange, Act, and Assert with blank lines. Name the behavior and expected outcome.
- Use Scaffold, Infra, Secrets, and CI suites. Nest `when ...` contexts and `it ...` outcomes.
- Keep actions and assertions visible. Put reusable setup and process execution in helpers.
- Each context must work with `go test -run`. Do not depend on sibling scenarios.
- Assert cleanup explicitly; keep `t.Cleanup` as the resource safety net.
