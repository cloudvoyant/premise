# {{PROJECT_NAME}} Development Guide

Generated from {{TEMPLATE_NAME}} v{{TEMPLATE_VERSION}}.

## Prerequisites

- [mise](https://mise.jdx.dev/) — manages the Odin compiler and all other tools
- **clang / LLVM (17–22)** on Linux — Odin's backend links via clang. `mise run install`
  installs it on CI Linux runners; install it yourself for local Linux dev. macOS uses
  the system/Homebrew clang.
- [gh CLI](https://cli.github.com/) for publishing GitHub releases

## Getting Started

```bash
mise install          # install Odin + node + shellcheck + shfmt
mise run install      # npm plugins + clang/LLVM (CI Linux)
mise run build        # debug build → bin/<project>
mise run test         # run @(test) procs
```

## Project Structure

```
src/lib.odin          # Library procs + inline @(test) procs
src/main.odin         # Entry point (same package), prints baked version
mise.toml             # Task runner and tool versions
version.txt           # Single source of truth for the version (no manifest)
```

## Development Workflow

1. **Write code** in `src/lib.odin` (library procs) or `src/main.odin` (entry)
2. **Write tests** as `@(test)` procs taking `t: ^testing.T`
3. **Run tests**: `mise run test`
4. **Lint**: `mise run lint` (`odin check src -vet -strict-style`)
5. **Format**: no-op — Odin ships no formatter (see CLAUDE.md; `odinfmt` is opt-in)

## Publishing (source, not binaries)

Odin has no package manager and no binary-library convention, so this project distributes
**source**: the git tag and its GitHub release (with the source archive GitHub attaches
automatically) are the release. No cross-compiled binaries are published — Odin links via
the host linker, so foreign targets can't link reliably on a single runner (and a bad
cross-link can exit 0 with an unusable binary, odin-lang/Odin#4821).

1. Ensure `GH_TOKEN` or `GITHUB_TOKEN` is set
2. Push to `main` — CI runs `mise run upversion` (creates the release) then `mise run publish`
3. `mise run publish` just ensures the source release exists (idempotent); it uploads nothing
4. `mise run publish:rc` cuts a source pre-release for testing

If you need an application binary for a specific platform, run `mise run build:prod` on a
matching runner and attach it to the release yourself.

## Consuming as a Library

Odin has no package manager. Vendor `src/` or add it as a git submodule, then wire it with
`-collection:name=path` and `import name "name:..."`. The git tag is the distribution.
