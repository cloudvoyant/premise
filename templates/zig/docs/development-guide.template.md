# {{PROJECT_NAME}} Development Guide

Generated from {{TEMPLATE_NAME}} v{{TEMPLATE_VERSION}}.

## Prerequisites

- [mise](https://mise.jdx.dev/) — manages Zig and all other tools
- Zig 0.16.0 is installed automatically by mise via `mise install`
- [gh CLI](https://cli.github.com/) for publishing GitHub releases

## Getting Started

```bash
mise install          # install Zig 0.16.0 + node + shellcheck + shfmt
mise run build        # debug build
mise run test         # run tests
```

## Project Structure

```
src/lib.zig           # Library module
src/main.zig          # CLI entry point
build.zig             # Build configuration
build.zig.zon         # Package manifest
mise.toml             # Task runner and tool versions
```

## Development Workflow

1. **Write code** in `src/lib.zig` (library logic) or `src/main.zig` (CLI)
2. **Write tests** inline as `test "name" { ... }` blocks in `lib.zig`
3. **Run tests**: `mise run test`
4. **Check format**: `mise run format:check`; fix with `mise run format`

## Cross-Platform Compilation

```bash
mise run build:all-platforms
# Outputs to zig-out/release/{target}/bin/<project-name>
```

## Publishing

1. Ensure `GH_TOKEN` or `GITHUB_TOKEN` is set
2. Push to `main` — CI runs `mise run upversion` then `mise run publish`
3. Manual: `mise run publish` (builds all platforms, creates GitHub release)

## Adding Dependencies

```zig
// build.zig.zon — add under .dependencies
.dependencies = .{
    .my_dep = .{
        .url = "https://github.com/org/repo/archive/abc123.tar.gz",
        .hash = "...",
    },
},
```

Then run `zig build --fetch` to validate and populate the hash.
