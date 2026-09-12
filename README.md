# premise

premise is an opinionated monorepo lifecycle toolkit built on mise: scaffold, update, and connect — conventions, CI, and AI skills for monorepo projects.

## Features

- **Monorepo scaffolding** — Scaffold monorepo projects from templates driven by interactive questionnaires
- **Automated updates** — Keep descendant projects up to date with changes from the templates they were scaffolded from
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

Release automation is tracked in [issue #2](https://github.com/cloudvoyant/premise/issues/2).

## Documentation

- [User Guide](docs/user-guide.md) - Complete setup and usage guide
- [Architecture](docs/architecture.md) - Design and implementation details
- [Infrastructure](docs/infrastructure.md) - Infrastructure and CI/CD details

## References

- [mise - dev tool manager](https://mise.jdx.dev/)
- [GoReleaser](https://goreleaser.com/)
- [bats-core bash testing](https://bats-core.readthedocs.io/)
- [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html)
- [Conventional Commits](https://www.conventionalcommits.org/)
- [GitHub Actions](https://docs.github.com/en/actions)
- [GCP Artifact Registry](https://cloud.google.com/artifact-registry/docs)
