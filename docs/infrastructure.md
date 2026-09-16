# Infrastructure

## Overview

`premise` is a [`mise`](https://mise.jdx.dev/)-powered project with testing and GitHub Actions CI. The `pm release` command owns stable versioning, tagging, and publication.

## Design

- Mise manages environment, dev tools, and tasks
- GitHub Actions drives CI/CD using mise tasks
- Org-level secrets avoid per-project secret configuration
- The build system is project-structure agnostic — only mise tasks need to work

## Implementation

### Mise For Environment & Tasks

Mise is the environment management tool and task runner. Since mise can manage a large array of languages and tools, it is a sensible choice for a language-agnostic build system that hooks into CI/CD and can be modified for any language.

Environment is configured in `mise.toml` under `[env]`:

```toml
[env]
GCP_REGISTRY_PROJECT_ID = "your-project-id"
GCP_REGISTRY_REGION     = "us-central1"
GCP_REGISTRY_NAME       = "your-repository-name"
```

### GitHub Actions For CI/CD

`.github/workflows/on-commit.yml` verifies pull requests and feature-branch pushes through the root `action.yml`; a feature-branch push whose HEAD commit message contains the exact marker `[publish-rc]` also runs the opt-in RC step, which only echoes the Go skip message. `.github/workflows/on-merge.yml` validates the trunk and calls `pm release`. Premise then reuses or creates the stable tag and publishes the GitHub release. `.github/workflows/on-deploy.yml` exposes the deploy flow.

Version calculation uses the svu Go SDK through `core/version.go`. Premise owns the stable-tag policy, so repositories do not carry `.svu.yml`. A `v0.0.0` stable bootstrap tag must exist before CI runs; it is created externally, never by a task or workflow. GoReleaser configuration is also generated inside Premise and written to a temporary file only while `pm release` runs. Calculated versions and generated configuration are never committed to source.

The `feature` flow detects a repository-root `premise.yaml` that declares at least
one template and runs `pm template test` as the authoritative check, so a
template-registry repository (for example `cloudvoyant/premise-cargo`) fails CI
when any declared template contract breaks. The action can also build Premise
from the checked-out action source via the `build-premise-from-source` input,
which is used by registries that predate a published release; ordinary consumers
keep the `install.sh` release bootstrap.

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
