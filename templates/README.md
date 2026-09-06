# Template Catalog

Templates are sets of language-specific override and extension files that are layered over the language-agnostic base during scaffold. Each template implements a required task contract so scaffolded projects behave consistently regardless of language.

## Available Templates

| Name   | Language   | Registry                 | Description                                                |
| ------ | ---------- | ------------------------ | ---------------------------------------------------------- |
| `uv`   | Python     | PyPI                     | Python library using uv, ruff, pytest                      |
| `zig`  | Zig        | GitHub Releases + GCP    | Zig library/binary with cross-platform build               |
| `go`   | Go         | GitHub Releases          | Go library/CLI with cross-platform build                   |
| `rust` | Rust       | GitHub Releases          | Rust library/binary; git-install + cross-platform binaries |
| `odin` | Odin       | GitHub Releases (source) | Odin source library; git-tag/source releases, no binaries  |
| `pnpm` | TypeScript | npm                      | TypeScript library using pnpm, vitest, ESLint, Prettier    |

## Task Contract

Every template **must** implement all of the following mise tasks:

| Task           | Description                                        |
| -------------- | -------------------------------------------------- |
| `build`        | Compile or prepare the project for local use       |
| `test`         | Run the full test suite                            |
| `lint`         | Run static analysis / linter                       |
| `lint-fix`     | Run linter with auto-fix                           |
| `format`       | Format source code in-place                        |
| `format-check` | Check formatting without modifying files (CI mode) |
| `publish`      | Publish package to the appropriate registry        |
| `upversion`    | Bump version (delegates to `mise-tasks/upversion`) |
| `version`      | Print current version                              |
| `version-next` | Compute next semantic version from commits         |

The base `mise.toml` provides stub implementations of all these tasks. Templates override them with language-specific implementations.

## Files a Template May Provide

Templates can provide any files their language requires. Non-exhaustive list:

- `mise.toml` — task overrides merged over the base
- `mise-tasks/*` — executable scripts implementing contract tasks
- `src/` — language-specific starter source code
- `CLAUDE.md.append` — appended to root `CLAUDE.md` during scaffold (see below)
- `docs/development-guide.template.md` — language-specific development guide (processed to `.md` during scaffold)
- `.releaserc.json` — semantic-release config for the language's registry
- Language manifests: `pyproject.toml`, `build.zig`, `build.zig.zon`, etc.
- `install.sh.template` — binary installer script (processed to `install.sh` during scaffold)
- Package manager configs, linter configs, etc.

## Files a Template Should Not Override

The following files are scaffold infrastructure. Do not override them unless there is a compelling language-specific reason:

- `mise-tasks/scaffold` — scaffold entrypoint
- `mise-tasks/utils` — shared logging and utility functions
- `mise-tasks/upversion` — version bumping logic

## CLAUDE.md Handling

`CLAUDE.md` is handled specially. Templates provide a `CLAUDE.md.append` file rather than overriding the root `CLAUDE.md`. During scaffold, the contents of `CLAUDE.md.append` are appended verbatim to the project's `CLAUDE.md`. This preserves base conventions (git, commit style, bash conventions) while allowing templates to add language-specific guidance.

`CLAUDE.md.append` must **not** include the `<!-- @context: test, bats -->` section — that section is template-development-specific and is stripped from scaffolded projects by the scaffold cleanup step.

## Adding a New Template

1. Create `templates/<name>/` directory
2. Implement all required tasks from the contract table above (in `mise.toml` and/or `mise-tasks/`)
3. Add language-specific files as needed
4. Create `CLAUDE.md.append` with language conventions (no bats/test-template context)
5. Run `mise run templates:list` to verify the new template is discovered
6. Add bats tests in `test/scaffold-templates.bats`
