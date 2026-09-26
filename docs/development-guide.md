# premise Development Guide

## Prerequisites

- [mise](https://mise.jdx.dev/) — manages Go and all other tools
- Go 1.25 is installed automatically by `mise install`
- [gh CLI](https://cli.github.com/) for publishing GitHub releases

## Getting Started

```bash
mise install          # Go 1.25 + node + prettier + shell tools (from mise.toml)
mise run build        # build bin/premise and its bin/pm alias
mise run test         # go test ./...
```

When mise is active, it prepends the repository's `bin/` directory to `PATH`. After `mise run build`, `pm` therefore uses the local development binary instead of any globally installed release while you are inside this repository.

## Project Structure

```
main.go               # CLI entry point (package main)
cmd/                   # Cobra command tree
core/                  # Public library surface, split into responsibility-focused modules
core/misex.go          # Mise command extension and sole executable boundary
core/osx.go            # Shared filesystem checks
core/bunx.go           # Generic package.json reading for Bun packages
core/cargox.go         # Generic Cargo manifest reading and version edits
core/package_metadata.go # Package-manager-neutral metadata
core/package_manager_plugin.go # Plugin contract, registration, and selection
core/release_plugins.go # Release sequencing across selected managers
core/plugins/go.go     # Go artifact policy
core/plugins/cargo.go  # Cargo metadata, artifact, and publication policy
core/plugins/bun.go    # Bun metadata and npm publication policy
cmd/package_manager_plugins.go # CLI composition of built-in plugins
core/ci.go             # Lifecycle selection and release-phase gating
core/release.go        # Shared stable/RC release preparation and publication
core/version.go        # Semantic version calculation and validation
go.mod                # Module manifest
mise.toml             # Task runner and tool versions
action.yml            # Published composite action (root — use a v0 tag while premise is in alpha)
.github/workflows/    # Own CI + published reusable workflows
```

## Development Workflow

1. **Write code** in `core/` (shared library logic), `core/plugins/` (package-manager integrations), or `cmd/` (commands)
2. **Write tests** as `*_test.go` files with `func TestXxx(t *testing.T)`
3. **Run tests**: `mise run test`
4. **Check format**: `mise run format:check`; fix with `mise run format`
5. **Lint**: `mise run lint` (`go vet ./...`)

## Consuming This Project

```bash
# CLI binary
curl -fsSL https://raw.githubusercontent.com/cloudvoyant/premise/main/install.sh | bash
go install github.com/cloudvoyant/premise@vX.Y.Z

# Library
go get github.com/cloudvoyant/premise@vX.Y.Z
```

```yaml
# CI action (published from this repo's root action.yml)
- uses: cloudvoyant/premise@v0
  with:
    flow: on-commit
```

The CLI composition root explicitly registers its built-in Go, Cargo, and Bun plugins before release flows. Library clients can call `core.RegisterPackageManagerPlugin(customPlugin)` before running a release; no dynamic loader or `init()` registration is required. `workspace.package_managers` selects plugins by ID in release order. File presence never selects a plugin. Managers with the same ecosystem conflict, so a workspace cannot enable Bun and pnpm together. GoReleaser builds and archives are combined, and each eligible package publisher receives the same version. Bun and Cargo plugins expose `GetPackageMetadata`, `ValidatePackage`, and `WillPublishOk`. Publication collects preflight errors across templates before making changes or publishing. Bun checks `NODE_AUTH_TOKEN` once and reuses one temporary credential file per registry. The action only sets up Mise, installs Premise, and calls `pm ci flow`. Set `install-premise` to `pre-built` to install a release, `build` to build the checked-out action source, or `skip` when `pm` is already on `PATH`.

```yaml
- uses: cloudvoyant/premise@<revision>
  with:
    flow: on-commit
    install-premise: build
```

Premise supports `on-commit`, `on-merge`, and `on-release` flows. A root Mise task with the same name overrides the convention-based fallback lifecycle. The flow command still owns guarded RC or stable publication after that lifecycle. Without an override, Premise uses `workspace.kind` to select the monorepo or template-registry lifecycle. A root can contain both generated projects and template declarations; the kind controls lifecycle behavior rather than content.

## Adding Dependencies

```bash
go get github.com/org/dep@v1.2.3
go mod tidy
```

## Publishing

Stable releases are owned by the release phase of `pm ci flow on-merge`, which delegates to the `pm release` implementation after validation. Merges to `main` run `.github/workflows/on-merge.yml`, which invokes that complete flow. Premise uses the svu Go SDK to calculate the version, creates and pushes the missing stable tag, generates temporary GoReleaser configuration, and publishes the release. Release candidates are not applicable to Go (prerelease installs resolve through commit hashes), so `mise run publish:rc` only echoes its skip message.

`pm version` exposes the same SDK calculations used by the release pipeline:

```bash
pm version current                  # current stable version
pm version next                     # next version from git history
pm version bump patch|minor|major   # explicit bump
pm version rc --identifier <id>     # MAJOR.MINOR.PATCH-rc.<id>
```

A `v0.0.0` stable bootstrap tag must exist before CI runs; it is created externally and is not produced by any task or workflow.
