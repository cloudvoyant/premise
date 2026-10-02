# Infrastructure

## Overview

`premise` is a [`mise`](https://mise.jdx.dev/)-powered project with testing and GitHub Actions CI.

## Repository infrastructure

- `mise.toml` defines the development tools and repository tasks.
- `action.yml` defines the published GitHub Action.
- `.github/workflows/` contains the repository workflows for checks, merges, and deploys.
- `install.sh` installs released CLI binaries.
- `go.mod` and `go.sum` define the Go module dependencies.

## Implementation

### Mise

Mise installs the pinned tools and runs repository tasks. The repository stores its environment values and task definitions in `mise.toml`.

### GitHub Actions For CI/CD

`.github/workflows/on-commit.yml` verifies pull requests and feature-branch pushes through the root `action.yml`; for a feature-branch push whose HEAD commit contains `[publish-rc]`, the `on-commit` flow also invokes the opt-in RC task. For Go, that task only prints the standard skip message. `.github/workflows/on-merge.yml` delegates the complete trunk lifecycle to the `on-merge` flow, which invokes the stable release phase after validation. `.github/workflows/on-deploy.yml` exposes the deploy flow.

The workflows run repository checks through `mise`. The merge workflow runs the stable release after validation. The deploy workflow runs the deploy flow. Generated commit and merge workflows grant `id-token: write` so npm can request a GitHub OIDC token for trusted publishing or provenance. The commit flow publishes release candidates only on marked feature-branch pushes, not on pull requests. The action supports `pre-built`, `build`, and `skip` installation modes.

### CI/CD Secrets

Org-level secrets are utilized to avoid the need for setting up secrets for every new project. This means setup is only needed once.

For GCP (default):

- `GCP_SA_KEY` - Service account JSON key
- `GCP_REGISTRY_PROJECT_ID`, `GCP_REGISTRY_REGION`, `GCP_REGISTRY_NAME` - Registry configuration

For other registries:

- npm: `NPM_TOKEN`
- PyPI: `PYPI_TOKEN`

### Cross-Platform Support

The project works on macOS, Linux, and Windows (via WSL) without requiring platform-specific tools.

Key compatibility measures:

- Mise handles installation of tools across host platforms
- Line endings enforced to LF via `.editorconfig`
- Bash 3.2+ required (macOS ships with Bash 3.2)

## References

- [mise - the dev tool manager](https://mise.jdx.dev/)
- [GitHub Actions](https://docs.github.com/en/actions)
- [GCP Artifact Registry](https://cloud.google.com/artifact-registry/docs)
- [Conventional Commits](https://www.conventionalcommits.org/)
