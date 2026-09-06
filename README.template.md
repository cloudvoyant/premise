# {{PROJECT_NAME}}

{{PROJECT_NAME}} is [add your project description here].

## Features

- [List key features of your project]

## Requirements

- bash 3.2+
- [mise](https://mise.jdx.dev/getting-started.html)

Run `mise install` to install all tools, then `mise run install` for any additional dependencies.

## Quick Start

```bash
git clone <your-repo>
cd {{PROJECT_NAME}}
mise install
```

Type `mise tasks` to see all available tasks:

```bash
❯ mise tasks
build        Build the project
clean        Clean build artifacts
install      Install dependencies
publish      Publish package to registry
run          Run project locally
test         Run tests
...
```

Build, run, and test with `mise run`:

```bash
mise run run
mise run test
```

Task dependencies run automatically — `mise run test` runs `build` first!

Commit using conventional commits (`feat:`, `fix:`, `docs:`). Merge/push to main and CI/CD will run automatically bumping your project version and publishing a package.

## Documentation

- [User Guide](docs/user-guide.md) - Complete setup and usage guide
- [Architecture](docs/architecture.md) - Design and implementation details
- [Infrastructure](docs/infrastructure.md) - Infrastructure and CI/CD details

## References

- [mise - dev tool manager](https://mise.jdx.dev/)
- [semantic-release](https://semantic-release.gitbook.io/)
- [bats-core bash testing](https://bats-core.readthedocs.io/)
- [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html)
- [Conventional Commits](https://www.conventionalcommits.org/)
- [GitHub Actions](https://docs.github.com/en/actions)
- [GCP Artifact Registry](https://cloud.google.com/artifact-registry/docs)

---

**Template**: {{TEMPLATE_NAME}} v{{TEMPLATE_VERSION}}
