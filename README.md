# premise

premise is an opinionated monorepo lifecycle toolkit built on mise: scaffold, update, and connect — conventions, CI, and AI skills for monorepo projects.

## Features

- **Monorepo scaffolding** — Scaffold monorepo projects from templates driven by interactive questionnaires
- **Safe workspace-root merging** — Expand explicit `template_registry.workspace_files` patterns against registry-root files, merge those files into the client workspace, and copy the selected template to `apps/<name>` or `libs/<name>`. Root conflicts use typed `mise.toml` rules, ordered registry-first Git policy, or complete-file choices
- **Future template migration** — Planned support for updating existing projects from newer template versions; generation remains create-only today
- **Self-updates** — Update the installed CLI to the latest release with `pm update`, or install a specific release with `pm update vX.Y.Z`
- **mise tooling** — Convenience tools around mise-based monorepo management, including vendoring scripts
- **Opinionated CI conventions** — Standardized setup and conventions for CI: variable naming and storage for artifact repositories, registries, Terraform backends, and more
- **CI backends** — Provider-specific CI configuration for correct builds, releases, and deployments by default. premise invokes the named mise tasks that each project provides, so projects do not maintain provider glue.
- **AI skills** — Skills that hook into CI and tasks for projects in the monorepo, without duplicating them into each client repo

## Requirements

- bash 3.2+
- [mise](https://mise.jdx.dev/getting-started.html)

Run `mise install` to install all development tools.

## Quick Start

```bash
git clone <your-repo>
cd premise
mise install
```

Type `mise tasks` to see all available tasks:

```bash
❯ mise tasks
build         Build the CLI
dev           Run the CLI from source
e2e           Run end-to-end tests
format        Format source files
format:check  Check formatting without changing files
install       Install development dependencies
lint          Run static analysis
lint:fix      Apply safe lint fixes
test          Run all Go tests
```

Build, run, and test with `mise run`:

```bash
mise run dev -- --help
mise run test
mise run e2e
```

Update an installed release in place:

```bash
pm update
pm update v0.2.1
```

## Documentation

- [User Guide](docs/user-guide.md) - Complete setup and usage guide
- [Architecture](docs/architecture.md) - Overall Premise architecture
- [Generation Architecture](docs/generation.md) - Create-only generation and template merging
- [Infrastructure](docs/infrastructure.md) - Infrastructure and CI/CD details

## References

- [mise - dev tool manager](https://mise.jdx.dev/)
- [GoReleaser](https://goreleaser.com/)
- [bats-core bash testing](https://bats-core.readthedocs.io/)
- [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html)
- [Conventional Commits](https://www.conventionalcommits.org/)
- [GitHub Actions](https://docs.github.com/en/actions)
- [GCP Artifact Registry](https://cloud.google.com/artifact-registry/docs)

## Pulumi environments and application secrets

Clients own their Pulumi programs under `infra/` and choose the language and tool version:

```sh
pm init --infra --infra-language go --pulumi-version 3.268.0
mise run infra:install
pm infra init --env preview --branch feature/example
pm infra plan --env preview --branch feature/example
```

Configure an existing remote backend before initialization. One program serves separate environment and branch-preview stacks.
Applications expose optional migration tasks. The environment CI flow orders migrations, infrastructure updates, application deployment, health checks, and recovery. Clients apply database infrastructure changes separately before schema migrations.
Local application secrets use fnox through Mise. Deployment credentials use OAuth or protected CI secrets.
See [infrastructure deployment](docs/modules/infrastructure.md) and [secret management](docs/secret-management.md).
