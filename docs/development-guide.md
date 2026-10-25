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
core/package_manager_backend.go # Backend contract, registration, and selection
core/release_backends.go # Release sequencing across selected managers
core/backends/go.go     # Go artifact policy
core/backends/cargo.go  # Cargo metadata, artifact, and publication policy
core/backends/bun.go    # Bun metadata and npm publication policy
core/backends/register.go # Registration of built-in backends
core/ci.go             # Lifecycle selection and release-phase gating
core/release.go        # Shared stable/RC release preparation and publication
core/version.go        # Semantic version calculation and validation
docs/modules/          # Per-module design, API usage, and implementation details
go.mod                # Module manifest
mise.toml             # Task runner and tool versions
action.yml            # Published composite action (root — use a v0 tag while premise is in alpha)
.github/workflows/    # on-commit, on-deploy, and on-merge only
```

## Development Workflow

1. **Write code** in `core/` (shared library logic), `core/backends/` (package-manager backends), or `cmd/` (commands)
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

The CLI composition root explicitly registers its built-in Go, Cargo, and Bun backends before release flows. Library clients can call `core.RegisterPackageManagerBackend(customBackend)` before running a release; no dynamic loader or `init()` registration is required. `workspace.package_managers` selects backends by ID in release order. File presence never selects a backend. Managers with the same ecosystem conflict, so a workspace cannot enable Bun and pnpm together. GoReleaser builds and archives are combined, and each eligible package publisher receives the same version. Cargo app templates produce GoReleaser archives by default, independently of the `publish` value in `Cargo.toml`. A direct Cargo app with a regular `tauri.conf.json` in its template root is automatically excluded from generic GoReleaser archives because Tauri builds its native installers separately. This does not disable package or template tasks. Bun and Cargo backends expose `GetPackageMetadata`, `ValidatePackage`, and `WillPublishOk`. Publication collects preflight errors across templates before making changes or publishing. Bun checks `NODE_AUTH_TOKEN` once and reuses one temporary credential file per registry. The action only sets up Mise, installs Premise, and calls `pm ci flow`. Set `install-premise` to `pre-built` to install a release, `build` to build the checked-out action source, or `skip` when `pm` is already on `PATH`.

```yaml
- uses: cloudvoyant/premise@<revision>
  with:
    flow: on-commit
    install-premise: build
```

Premise supports `on-commit`, `on-merge`, and `on-deploy` lifecycle flows. A root Mise task with the same name runs before project checks. Publish RC or stable artifacts after the selected checks pass. An explicit environment selects the environment part of the lifecycle. It calls the infrastructure, migration, and deployment modules. Without an override, Premise uses `workspace.kind` to select the monorepo or template-registry lifecycle. A root can contain both generated projects and template declarations; the kind controls lifecycle behavior rather than content.

A scoped `pm ci flow` builds native files for a declared project and platform. Pass `--project`, `--platform`, `--channel` (`stable` or `rc`), `--version`, and an empty absolute `--output-dir`. The project's `release:build` Mise task receives `RELEASE_VERSION`, `PREMISE_RELEASE_CHANNEL`, and `PREMISE_ARTIFACT_DIR` and must write nonempty files there. The project owns its build commands and version overrides. The flow stages files without publishing packages or creating a tag.

The commit and merge workflows call the composite action with `command: plan`, `flow`, `flow-matrix`, `release`, and `packages`. GitHub selects runners and transfers native files; the action owns each Premise operation. The publishing job validates the complete file set and creates or reuses a tag at the triggering commit before publishing ordinary archives and native installers together. Pin external action calls to a verified release tag or commit. Docker registry publishing is not part of this flow.

## Adding Dependencies

```bash
go get github.com/org/dep@v1.2.3
go mod tidy
```

## Publishing

Commit and merge workflows finish checks before publication. The release job calls `pm release` to create or reuse the shared tag and publish the GitHub release. Before creating a stable tag, Premise builds a GoReleaser snapshot without publication credentials. A failed build leaves no new tag. If the tag already points at HEAD, a rerun resumes publication.

A feature push with `[publish-rc]` requests a GitHub prerelease after checks pass. Go libraries use repository tags for stable and prerelease versions. The root `publish:rc` task does not upload Go packages to a separate registry.

Generated workflows use `cloudvoyant/premise@v0`. After a successful stable GitHub release, CI runs `mise run publish:update-action-tags` with `RELEASE_VERSION`. This task moves the major action tag to the released commit. Version tags remain fixed. RC publication does not move the major tag.

When a template adds manifest fields, use a Premise action that can read those fields. For tandem validation, pin the dependent registry workflow to a compatible released RC. Generated client workflows retain the stable major reference and need the core stable release before use. Test generated client workflows as well as registry workflows before merging.

A library installed directly from Git can use `workspace.package_managers: []`. An omitted list has the same meaning. Premise publishes the version tag and GitHub release without a language backend, compiled assets, registry credentials, or a custom publication hook. The package phase reports a successful skip. See [Libraries installed from Git](modules/ci.md#libraries-installed-from-git) for a Lua example.

For this configuration, `pm release` plans and publishes a stable release. `pm release --build` reports a successful skip because there are no artifacts to build. The composite action uses the existing `command: plan`, `command: release`, and `command: packages` operations. Source-only releases use GitHub's source downloads and do not attach an empty checksum file.

To check source-only release configuration with the pinned GoReleaser version locally, run:

```bash
PREMISE_TEST_GORELEASER="$(mise where goreleaser@2.18.1)/goreleaser" \
  go test ./e2e -run TestSourceOnlyConfigWithGoReleaser -v
```

`pm version` exposes the same SDK calculations used by the release pipeline:

```bash
pm version current                  # current stable version
pm version next                     # next version from git history
pm version bump patch|minor|major   # explicit bump
pm version rc --identifier <id>     # MAJOR.MINOR.PATCH-rc.<id>
```

## Infrastructure command tests

Run `mise run e2e:infra` separately on demand with a dedicated GCP project and an existing GCS state backend. The [static GCS fixture](../e2e/testdata/pulumi/README.md) uses `pm ci` flows across environments. Infra apply creates a bucket. Deployment uploads a static page. A fake migration writes a marker. The same fixture tests automatic rollback and cleanup. Infrastructure validation does not run in CI.

## Complexity checks

`mise run lint` and `mise run lint:fix` enforce a maximum cyclomatic complexity of 30 per function in package-manager backends, dependency merging, Mise composition, and generation. Test files are excluded. The check uses Gocyclo 0.6.0 installed through Mise and fails when a function exceeds the limit. Other modules retain their existing Go vet and ShellCheck checks.
