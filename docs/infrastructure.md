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

`.github/workflows/on-commit.yml` verifies pull requests and feature-branch pushes through the root `action.yml`; for a feature-branch push whose HEAD commit contains `[publish-rc]`, the `on-commit` flow also invokes the opt-in RC task. For Go, that task only prints the standard skip message. `.github/workflows/on-merge.yml` delegates the complete trunk lifecycle to the `on-merge` flow, which invokes the stable release phase after validation. `.github/workflows/on-deploy.yml` exposes the deploy flow.

Version calculation uses the svu Go SDK through `core/version.go`. Premise owns the stable-tag policy, so repositories do not carry `.svu.yml`. A `v0.0.0` stable bootstrap tag must exist before CI runs; it is created externally, never by a task or workflow. GoReleaser configuration is also generated inside Premise and written to a temporary file only while `pm release` runs. Calculated versions and generated configuration are never committed to source.

The action delegates lifecycle and publication policy to `pm ci flow`. The supported flows are `on-commit`, `on-merge`, and `on-release`. A matching root Mise task replaces the fallback lifecycle, but the flow still owns its guarded publication phase. Otherwise, Premise runs the lifecycle selected by `workspace.kind`: monorepo lifecycle tasks or each declared registry template. A repository can contain both generated projects and `template_registry`; the kind selects lifecycle behavior rather than forbidding either capability.

The action accepts one `install-premise` mode. `pre-built` uses `install.sh`, `build` compiles the checked-out action source, and `skip` requires an existing `pm` on `PATH`. Real RC and stable publication remain in separate credential-bearing workflow steps.

### Package and Artifact Publication Boundaries

Package-manager plugins select registry packages separately from application artifacts. Premise retains version planning, tagging, release order, and credentials. A Cargo direct package reaches crates.io only when `[package] publish` permits it. A matching direct application reaches generic GoReleaser even when its registry publication is disabled. A nested Tauri package uses neither direct path; its workflow and template task publish native installers.

Bun packages with `private: false` and `publishConfig.registry` use their template publish tasks. Public and restricted registry visibility are separate from publish eligibility. Bun has no configured native GitHub archives, so the artifact step skips without blocking npm publication. Static-site uploads, OCI images, and deploy targets are not inferred from `kind: app`; they require explicit publication configuration. A template with no selected registry or artifact destination remains unpublished.

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
