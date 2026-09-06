# Architecture

## Overview

`mise-lib-template` is a [`mise`](https://mise.jdx.dev/)-powered [1] language-agnostic template for building projects with automated versioning, testing, and GitHub Action powered CI/CD workflows.

## Design

- mise managed environment, dev, tools, etc.
- mise is used for running tasks
- certain tasks like `build:prod`, `test`, `publish`, etc. are used by GitHub Actions
- tasks can be overridder for your specific needs
- the build system is project structure agnostic, all that matters is that mise tasks work

## Implementation

### Mise For Environment & Tasks

Mise is the environment management tool and task runner for projects based on this template. Since mise can manage a large array of languages and tools, its a sensible choice for a language agnostic build system that hooks into CI/CD, and can easily be modified for any language.

### GitHub Actions For CI/CD

At this time this template is focused on GitHub usage, but it could easily be adapted to GitLab, etc. by hooking into appropriate mise tasks.

The `ci` worflow runs on feature branch commits and publishes pre-release packages for testing. The `release` workflow runs on merge to main, and is where `semantic-release` is used to bump versions and update the changelog.

### Claude Commands For Adapting / Upgrading

Claude commands provide LLM-assisted workflows for complex tasks. This is utilized to support adapting the template to any use-case, and upgrading to newer versions since both of these tasks are hard to accomplish with simpler scripting.

- `/adapt` - Template-only command for adapting to new languages (auto-deletes after use)
- `/upgrade` - Upgrade to the latest template version

### CI/CD Secrets

Org-level secrets are utilized to avoid the need for setting up secrets for every new project. This means, setup is only needed once.

For GCP (default):

- `GCP_SA_KEY` - Service account JSON key
- `GCP_REGISTRY_PROJECT_ID`, `GCP_REGISTRY_REGION`, `GCP_REGISTRY_NAME` - Registry configuration

For other registries:

- npm: `NPM_TOKEN`
- PyPI: `PYPI_TOKEN`

### Cross-Platform Support

The template works on macOS, Linux, and Windows (via WSL) without requiring users to install platform-specific tools. This broad compatibility reduces team onboarding friction and prevents "works on my machine" issues.

Key compatibility measures:

- Mise handles installation of tools across host platforms
- Line endings enforced to LF via `.editorconfig` (prevents git diff noise on Windows)
- `sed_inplace` is used for scaffodling (text-replacement) and handles differences between macOS and GNU sed (abstracts platform quirks)
- Bash 3.2+ required (macOS ships with Bash 3.2, avoiding Bash 4+ features ensures compatibility without upgrades)

## Template System

### Template Overlay Design

The `templates/` directory contains language-specific override and extension files that are layered over the agnostic base during scaffold:

1. Base scaffold runs first — copies all base files to the destination, excluding `templates/`, `test/`, and other dev-only paths
2. Template overlay — if `--template <name>` is given, `rsync` overlays `templates/<name>/` over the scaffolded output (excluding `CLAUDE.md.append` and `README.md`)
3. CLAUDE.md merge — `templates/<name>/CLAUDE.md.append` is appended (not replaced) to the output `CLAUDE.md` after the bats/test section is stripped
4. Name replacement — sed replaces every case variant of `mise_lib_template`/`mise-lib-template` with the project name in overlaid files, and rewrites the module-path org segment (`github.com/cloudvoyant/<repo>`) to the value of `--github-org` (default: derived from the git remote, else a placeholder). The rewrite is scoped to the project's own repo path so unrelated links are left intact. Package directories under `src/` are renamed via `mv` to the language's idiomatic case: `src/mise_lib_template/` → the project's snake_case name (Python), `src/miselibtemplate/` → its flatcase name (Go)
5. Agnostic cleanup — `src/sample-code.txt` and `src/.gitkeep` are removed when a template is applied

Files templates must NOT override (scaffold infrastructure):

- `mise-tasks/scaffold` — scaffold entrypoint
- `mise-tasks/utils` — shared logging/utilities
- `mise-tasks/upversion` — versioning logic

`CLAUDE.md` is always merged, never replaced. Templates must provide `CLAUDE.md.append` instead.

### Task Contract

All templates must implement the same mise tasks so base GitHub Actions workflows work without modification:

| Task           | Description                       |
| -------------- | --------------------------------- |
| `build`        | Compile / build artifacts         |
| `test`         | Run test suite                    |
| `lint`         | Static analysis                   |
| `lint:fix`     | Auto-fix lint issues              |
| `format`       | Format source in-place            |
| `format:check` | Check formatting (CI)             |
| `publish`      | Publish to registry               |
| `upversion`    | Bump version via semantic-release |
| `version`      | Print current version             |
| `version:next` | Preview next version              |

`jdx/mise-action@v4` reads `mise.toml` and installs all declared tools (uv, zig, etc.) automatically — no template-specific GitHub Actions files needed.

### Publishing Pipeline

| Template | Package            | Registry                 | Trigger  |
| -------- | ------------------ | ------------------------ | -------- |
| agnostic | mise-lib-template  | GCP Artifact Registry    | `v*` tag |
| uv       | mise-uv-template   | PyPI                     | `v*` tag |
| zig      | mise-zig-template  | GitHub Releases          | `v*` tag |
| go       | mise-go-template   | GitHub Releases          | `v*` tag |
| rust     | mise-rust-template | GitHub Releases          | `v*` tag |
| odin     | mise-odin-template | GitHub Releases (source) | `v*` tag |
| pnpm     | mise-pnpm-template | npm                      | `v*` tag |

All template packages share the same version tag as `mise-lib-template`. `templates:publish` scaffolds each template into `.tmp/`, sets the version, and calls `mise run publish` from the scaffolded project. The template's own `publish` task handles registry-specific logic.

## References

- [mise - the dev tool manager](https://mise.jdx.dev/)
- [semantic-release](https://semantic-release.gitbook.io/)
- [bats-core bash testing](https://bats-core.readthedocs.io/)
- [Google Shell Style Guide](https://google.github.io/styleguide/shellguide.html)
- [Conventional Commits](https://www.conventionalcommits.org/)
- [GitHub Actions](https://docs.github.com/en/actions)
- [GCP Artifact Registry](https://cloud.google.com/artifact-registry/docs)
