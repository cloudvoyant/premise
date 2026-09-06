# {{PROJECT_NAME}} Development Guide

Generated from {{TEMPLATE_NAME}} v{{TEMPLATE_VERSION}}.

## Prerequisites

- [mise](https://mise.jdx.dev/) — manages Go and all other tools
- Go 1.24 is installed automatically by mise via `mise install`
- [gh CLI](https://cli.github.com/) for publishing GitHub releases

## Getting Started

```bash
mise install          # install Go 1.24 + node + shellcheck + shfmt
mise run build        # go build ./...
mise run test         # go test ./...
```

## Project Structure

```
main.go               # CLI entry point (package main)
src/{{PROJECT_NAME}}/ # Library package
go.mod                # Module manifest
mise.toml             # Task runner and tool versions
```

## Development Workflow

1. **Write code** in `src/{{PROJECT_NAME}}/` (library logic) or `main.go` (CLI)
2. **Write tests** as `*_test.go` files with `func TestXxx(t *testing.T)`
3. **Run tests**: `mise run test`
4. **Check format**: `mise run format:check`; fix with `mise run format`

## Cross-Platform Compilation

```bash
mise run build:all-platforms
# Outputs dist/release/{{PROJECT_NAME}}-vVERSION-{target}.tar.gz per target
```

## Publishing

1. Ensure `GH_TOKEN` or `GITHUB_TOKEN` is set
2. Push to `main` — CI runs `mise run upversion` (creates the tag + Release) then
   `mise run publish` (uploads binaries)
3. Consume as a library with `go get <module>@vX.Y.Z`

## Adding Dependencies

```bash
go get github.com/org/dep@v1.2.3
go mod tidy
```
