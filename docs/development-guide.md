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
core/                  # Public library surface
go.mod                # Module manifest
mise.toml             # Task runner and tool versions
action.yml            # Published composite action (root — use a v0 tag while premise is in alpha)
.github/workflows/    # Own CI + published reusable workflows
```

## Development Workflow

1. **Write code** in `core/` (library logic) or `cmd/` (commands)
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
    flow: feature
```

A consumer that needs an unreleased Premise (for example a template registry
being bootstrapped before a release exists) can build the action from its
checked-out source instead of installing a published release:

```yaml
- uses: cloudvoyant/premise@<revision>
  with:
    flow: feature
    build-premise-from-source: "true"
```

When `build-premise-from-source` is `true` the action builds `github.action_path`
and exposes both `premise` and its `pm` alias on `PATH` for any calling
repository. The build resolves Go through Mise from the action's own
`mise.toml` (`mise exec -- go build`), which auto-installs the pinned Go
version, so a consumer repository does not need Go on its own toolchain. It
defaults to `false`, which keeps the `install.sh` release bootstrap for ordinary
consumers. In the `feature` flow the action also
detects a repository-root `premise.yaml` that declares at least one template and
runs `pm template test` as the authoritative registry check; repositories without
declared templates keep only the root lifecycle checks.

## Adding Dependencies

```bash
go get github.com/org/dep@v1.2.3
go mod tidy
```

## Publishing

Stable releases are versioned with `svu` (through the `pm version` command) and built by GoReleaser. Merges to `main` run `.github/workflows/on-merge.yml`, which computes the next stable version, creates and pushes the `vMAJOR.MINOR.PATCH` tag when one is missing, and publishes the GoReleaser release. Release candidates are not applicable to Go (prerelease installs resolve through commit hashes), so `mise run publish:rc` only echoes its skip message.

`pm version` exposes the svu calculations used by the release pipeline:

```bash
pm version current                  # current stable version
pm version next                     # next version from git history
pm version bump patch|minor|major   # explicit bump
pm version rc --identifier <id>     # MAJOR.MINOR.PATCH-rc.<id>
```

A `v0.0.0` stable bootstrap tag must exist before CI runs; it is created externally and is not produced by any task or workflow.
