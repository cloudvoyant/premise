# {{PROJECT_NAME}} Development Guide

Generated from {{TEMPLATE_NAME}} v{{TEMPLATE_VERSION}}.

## Prerequisites

- [mise](https://mise.jdx.dev/) — manages Rust and all other tools
- Rust 1.88 is installed by `mise install`; clippy, rustfmt, and cross targets are added by `mise run install`
- [gh CLI](https://cli.github.com/) for publishing GitHub releases
- Docker — required by `cross` for `mise run build:all-platforms`

## Getting Started

```bash
mise install          # Rust 1.88 + node + shell tools (from mise.toml)
mise run install      # clippy + rustfmt + cross targets + cross binary + npm sr plugins
mise run build        # debug build
mise run test         # run tests
```

## Project Structure

```
src/lib.rs            # Library crate (consumed via { git, tag })
src/main.rs           # CLI entry point (depends on the lib crate)
Cargo.toml            # Package manifest ([package] first)
Cargo.lock            # Committed lock for reproducible git-tag consumers
mise.toml             # Task runner and tool versions
```

## Development Workflow

1. **Write code** in `src/lib.rs` (library logic) or `src/main.rs` (CLI)
2. **Write tests** in `#[cfg(test)] mod tests { ... }` blocks
3. **Run tests**: `mise run test`
4. **Check format**: `mise run format:check`; fix with `mise run format`
5. **Lint**: `mise run lint` (clippy denies warnings)

## Consuming This Project (no crates.io)

```toml
# library dependency (Cargo pins the resolved commit in Cargo.lock)
[dependencies]
{{PROJECT_NAME}} = { git = "https://github.com/<org>/{{PROJECT_NAME}}", tag = "vX.Y.Z" }
```

```bash
# installable binary
cargo install --git https://github.com/<org>/{{PROJECT_NAME}} --tag vX.Y.Z
```

## Cross-Platform Compilation (Linux only in v1)

```bash
mise run build:all-platforms
# cross build --release for: x86_64/aarch64 × unknown-linux-{gnu,musl}
# Outputs target/dist/{{PROJECT_NAME}}-vVERSION-<triple>.tar.gz
```

macOS/Windows prebuilt binaries are a known gap — those users run `cargo install --git`.

## Publishing

1. Ensure `GH_TOKEN` or `GITHUB_TOKEN` is set (and Docker is available for `cross`)
2. Push to `main` — CI runs `mise run upversion` then `mise run publish`
3. `upversion` creates the release + tag; `publish` only uploads binaries to it
