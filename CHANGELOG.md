## [2.15.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.14.0...v2.15.0) (2026-07-15)
* emit placeholder docs during scaffold

Overwrite the platform's docs/architecture.md and docs/user-guide.md
with minimal, project-named starter skeletons so a scaffolded project
begins with clean placeholders rather than template-specific content
copied verbatim from this repo.

**scaffold**
- generate placeholder architecture.md and user-guide.md after name
  substitution and *.template.md processing, so $PROJECT_NAME is
  resolved and nothing regenerates the files afterward
- skip generation when the destination has no docs/ directory
- point the final "next steps" hint at docs/infrastructure.md for
  GitHub secrets instead of the removed user-guide reference

- drop the dangling user-guide.md#cicd-secrets links from
  architecture.md and infrastructure.template.md, which no longer
  resolve once docs are replaced with placeholders

**tests**
- add a scaffold.bats case asserting both docs are replaced with the
  project-named placeholders, the TODO body survives, and no
  template-only content leaks through
- retarget the go org-rewrite test at CONTRIBUTING.md now that the
  user-guide link it relied on is gone

Closes #7

## [2.14.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.13.0...v2.14.0) (2026-07-14)
* remove Docker and Compose support

Drop all Docker and Docker Compose containerization support from the
template and every language template.

Removed:
- Base Dockerfile, docker-compose.yml, .dockerignore
- Base docker:build/run/test mise tasks (mise-tasks/docker/)
- docker:build/run/test task scripts from the zig, go, rust, and odin
  templates
- docker contract-task entries and assert_docker_tasks helper from
  test/helpers/contract.bash
- test/docker.bats and all docker assertions in scaffold-templates.bats
- Docker references from CLAUDE.md, README, templates/README, adapt
  command, and docs

Note: rust's `cross` cross-compilation tooling (which uses Docker as a
build backend) is unrelated to containerization support and is retained.

templates:test passes (158 tests, 0 failures).

* list go/rust/odin (and pnpm) templates in top-level README

The repo README's use-case list, scaffold hint, and Templates table
still showed only agnostic/uv/zig. Add go, rust, odin, and pnpm, point
to templates/README.md for the full catalog, and fix a typo.

* update user-facing docs for go/rust/odin templates

Bring the top-level docs current with the go/rust/odin templates
(v2.13.0) and this session's scaffold changes. Docs-only.

- architecture.md: Publishing Pipeline table lists every template
  package; scaffold name-replacement covers flatcase Go packages and
  the scoped --github-org module-path rewrite
- user-guide.md: template table + Quick Start add go/rust/odin, a
  --github-org note, and a language-agnostic toolchain note; CI/CD
  secrets note odin ships source, not binaries
- CONTRIBUTING.md: migrate just -> mise, fix repo URL and plugin
  command names, drop the non-existent publish.yml

## [2.13.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.12.0...v2.13.0) (2026-07-13)
* add go language template

Go library/CLI template implementing the full task contract (build,
test, lint, format, publish, docker, version) with starter source, a
Dockerfile, and CLAUDE.md conventions. Builds a runnable binary; module
path is github.com/<org>/<project>; idiomatic flatcase package name.

* add odin language template

Odin template implementing the full task contract via the
github:odin-lang/Odin backend, with starter source, a Dockerfile, and
CLAUDE.md conventions. Distributes source (git tag / GitHub source
release) rather than binaries, matching Odin's no-package-manager
convention.

* add rust language template

Rust library/binary template implementing the full task contract with
cargo build/test, rustfmt/clippy, starter source, a Dockerfile, and
CLAUDE.md conventions. Distributes via git-tag install and
cross-platform release binaries.

* wire scaffold, tests, and docs for the new templates

Shared scaffold, test, and catalog changes supporting the go, rust, and
odin templates.

- scaffold: add --github-org flag/prompt (git-remote fallback) so
  module paths resolve to the user's org; render go packages as
  flatcase; skip the install.sh prompt for source-only templates
- tests: scaffold, docker, and template-export bats coverage for the
  three templates
- publish: attach the new templates to the template-package release
  flow
- docs: list the templates in the catalog README
- gitignore: keep the rust template's committed Cargo.lock

## [2.12.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.11.0...v2.12.0) (2026-07-10)
* move mise tasks to nested files, drop dev containers ([#2](https://github.com/cloudvoyant/mise-lib-template/issues/2))

- move all inline mise.toml tasks to nested file tasks under mise-tasks/
  (base + pnpm/uv/zig); namespaced task contract (docker:build, format:check, ...)
- bare build/test/run/publish still resolve via a _default file, so
  Dockerfile/docker-compose.yml need no change
- remove .devcontainer and all dev-container references (PL-10)
- scaffold self-deletes its entrypoint and strips mise-tasks/templates from
  scaffolded projects; overlay chmod recurses into nested task dirs
- timestamp RC versions (yyyymmddhhmm before the sha) for per-commit RC publishing
- bump actions/upload-artifact to v7
- update scaffold/contract/CI/docs; add override + executability test coverage

## [2.11.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.10.0...v2.11.0) (2026-07-09)
* add semantic-release and CI workflow rules to CLAUDE.md

* fix template issues (PL-5/6/7/8), Zig 0.16, start versions at v0

Resolves the open Platform issues, migrates the Zig template to the
latest stable toolchain, makes new libraries start their version
history at v0, and hardens the template publish pipeline so releases
work end to end.

**Linear issues**

- PL-5: scaffold now removes templates/ and test/ from generated
  projects (the rsync --exclude was a no-op in the in-place flow); an
  in-place bats test guards the regression
- PL-6: uv template install provisions semantic-release and carries
  _.path, so scaffolded uv projects can release
- PL-7: bump actions/checkout to v6 and jdx/mise-action to v4
- PL-8: rename .mise-tasks/ to mise-tasks/ across scaffold, task
  definitions, tests, and docs; add Prettier markdown linting under
  `lint` (format-check stays dependency-free for the scaffold contract
  test) and disable embedded-code formatting so doc examples are left
  as authored

**Zig 0.16**

- pin zig 0.16.0 and declare minimum_zig_version in build.zig.zon

**v0 versioning**

- seed a v0.0.0 baseline tag at the root commit in upversion when no
  final release tag exists, so the first release is 0.x not 1.0.0; the
  guard ignores -rc pre-release tags
- seed initial versions at 0.0.0 (version.txt and uv pyproject)

**Publish pipeline**

- pin pnpm to 10 and approve esbuild's build script; publish with
  mise's pnpm, not corepack (which pulls pnpm 11)
- authenticate npm via a user-level ~/.npmrc (pnpm 10 ignores a project
  .npmrc) and fail fast on an empty NPM_TOKEN
- publish RC packages to PyPI/npm (TestPyPI is unreachable from CI)
  with re-run-safe versions via GITHUB_RUN_ATTEMPT; idempotent GitHub
  pre-release; grant contents:write to the publish-rc job
- anchor the build.zig.zon version sed so it skips minimum_zig_version

Closes PL-5, PL-6, PL-7, PL-8

## [2.10.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.9.0...v2.10.0) (2026-03-18)

### Features

* add NPM_TOKEN secret name to pnpm publish docs

## [2.9.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.8.0...v2.9.0) (2026-03-18)

### Features

* clarify npm automation token requirement in pnpm docs

## [2.8.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.7.0...v2.8.0) (2026-03-18)

### Features

* note automation token requirement in pnpm publish docs


### Documentation

* update pnpm auth docs to use NODE_AUTH_TOKEN

## [2.7.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.6.2...v2.7.0) (2026-03-17)

### Features

* add .npmrc to pnpm template and unignore rc files

pnpm v10 requires NODE_AUTH_TOKEN via .npmrc placeholder for auth.
Remove blanket .npmrc/.pypirc gitignore rules that prevented
committing placeholder-only config files.

## [2.6.2](https://github.com/cloudvoyant/mise-lib-template/compare/v2.6.1...v2.6.2) (2026-03-16)

### Bug Fixes

* use NODE_AUTH_TOKEN for pnpm v10 npm publish auth

## [2.6.1](https://github.com/cloudvoyant/mise-lib-template/compare/v2.6.0...v2.6.1) (2026-03-16)

### Bug Fixes

* write npm auth token to .npmrc before pnpm publish

pnpm does not pick up NPM_TOKEN from the environment automatically.
Write the token to .npmrc in the scaffolded project dir before
calling mise run publish / pnpm publish.

## [2.6.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.5.0...v2.6.0) (2026-03-16)

### Features

* add pnpm TypeScript template with npm publishing

Add templates/pnpm/ implementing all 13 contract tasks using
TypeScript, vitest, ESLint (flat config), Prettier, and tsc.
Publishes to npm via pnpm; supports RC publishing with --tag next.

- Wire mise-pnpm-template into publish-templates and
  publish-templates-rc pipeline scripts
- Add NPM_TOKEN to release.yml and ci.yml workflow envs
- Add 20 bats tests (17 scaffold + 3 docker)
- Update templates/README.md and docs/user-guide.md with full
  npm setup section (tokens, OIDC trusted publishing, scopes)

## [2.5.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.9...v2.5.0) (2026-03-14)

### Features

* fix zig template post-scaffold issues

- Simplify build task (scaffold already strips stale fingerprint)
- Add npm install + _.path to install task so semantic-release works in CI
- Package release binaries as versioned tar.gz to match install.sh
- Rename VERSION→TAG in install.sh.template for clarity
- Strip GCP_SA_KEY guard from Publish package step for zig and uv templates
- Add zig-out/ and .zig-cache/ to base .gitignore


### Bug Fixes

* handle missing fingerprint in zig build task for Zig 0.15.1

Zig 0.15.1 errors with "suggested value: 0x..." when fingerprint is
absent (scaffold strips the stale value). Update the auto-fix to match
both "use this value" and "suggested value" patterns, and handle the
missing-line case by inserting the fingerprint before .paths with awk.

## [2.4.9](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.8...v2.4.9) (2026-03-08)

### Bug Fixes

* produce platform-suffixed zig release binaries to avoid upload conflicts

Copy each cross-compiled binary with its target name appended
(e.g. mise-zig-template-x86_64-linux) so all five platform assets
have unique filenames and can be uploaded to a GitHub release in
one gh release upload call without 404 conflicts.

Update publish to search maxdepth 1 for the suffixed files only.

## [2.4.8](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.7...v2.4.8) (2026-03-08)

### Bug Fixes

* run native build first in build-all-platforms to fix fingerprint

## [2.4.7](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.6...v2.4.7) (2026-03-08)

### Bug Fixes

* auto-fix build.zig.zon fingerprint after scaffold name substitution

Zig requires the fingerprint to match the package name hash. After scaffold
renames mise_lib_template → project name, the fingerprint becomes invalid.
The build task now detects this, extracts zig's suggested value, and retries.

* correct zig 0.15.x incompatibilities in template source files

- Update build.zig.zon fingerprint to match current package content
- Replace std.io.getStdOut() with std.debug.print (API removed in 0.15)
- Add build and test to RUNNABLE_TASKS to catch compile errors locally

## [2.4.6](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.5...v2.4.6) (2026-03-08)

### Bug Fixes

* update build.zig for Zig 0.15.x API (root_module replaces root_source_file)

## [2.4.5](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.4...v2.4.5) (2026-03-08)

### Bug Fixes

* add fingerprint field to build.zig.zon for Zig 0.15.x compatibility

## [2.4.4](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.3...v2.4.4) (2026-03-08)

### Bug Fixes

* make uv publish depend on build to produce dist/ before publishing

## [2.4.3](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.2...v2.4.3) (2026-03-08)

### Bug Fixes

* mkdir dest dirs before scaffolding in publish-templates

scaffold requires --dest to exist; publish-templates only created the
parent .tmp/publish-templates/ but not the per-template subdirectories.

## [2.4.2](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.1...v2.4.2) (2026-03-08)

### Bug Fixes

* remove publishCmd from releaserc to fix CI failure

Template publishing is handled by the dedicated CI step which has
GH_TOKEN and UV_PUBLISH_TOKEN set. The publishCmd ran in the upversion
step where those env vars are unavailable, causing semantic-release to
throw a lodash ReferenceError.

## [2.4.1](https://github.com/cloudvoyant/mise-lib-template/compare/v2.4.0...v2.4.1) (2026-03-08)

### Bug Fixes

* remove UV_PUBLISH_TOKEN from lodash template in releaserc

Lodash template throws ReferenceError if the env var is undefined.
UV_PUBLISH_TOKEN is passed to uv publish via the shell environment
automatically when set as a CI secret — no explicit interpolation needed.

## [2.4.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.3.0...v2.4.0) (2026-03-08)

### Features

* align templates with source files and streamline docs

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>

## [2.3.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.2.0...v2.3.0) (2026-03-08)

### Features

* align templates with source files and streamline docs

## [2.2.0](https://github.com/cloudvoyant/nv-lib-template/compare/v2.1.0...v2.2.0) (2026-03-07)

### Features

* migrate commands and tooling to mise, add style guide

- Rename template to mise-lib-template
- Update Claude commands (upgrade, adapt) with mise/2.x guidance
- Add Zed editor config, drop VS Code support
- Add CLAUDE.md context-tagged style guide at repo root
- Add .style/ infrastructure for style validation
- Remove toggle-files task
- Fix script header ordering and stale path references
- Fix tests for CLAUDE.md relocation to repo root

## [2.1.0](https://github.com/cloudvoyant/mise-lib-template/compare/v2.0.1...v2.1.0) (2026-03-07)

### Features

* simplify Docker and devcontainer setup using mise cookbook patterns

- Switch devcontainer to standard mcr.microsoft.com/devcontainers/base:ubuntu
  with ghcr.io/devcontainers-extra/features/mise:1
- Simplify Dockerfile to single-stage ubuntu:22.04 with minimal APT
  dependencies (curl, ca-certificates, python3)
- Remove custom devcontainer build in favor of standard feature-based setup
- Move publish, hide, show tasks from .mise-tasks/ files to mise.toml
- Update architecture docs to reflect simplified approach


### Documentation

* replace just and direnv references with mise in README

* replace just/direnv references with mise equivalents

## [2.0.1](https://github.com/cloudvoyant/mise-lib-template/compare/v2.0.0...v2.0.1) (2026-03-06)

### Bug Fixes

- add env block to Authenticate step so GCP_SA_KEY condition passes

The if condition checks env.GCP_SA_KEY != '' but without an env: block
the secret is invisible to the expression evaluator, causing the step
to be skipped while Setup gcloud and Publish (which have the env block)
still run unauthenticated.

## [2.0.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.16.0...v2.0.0) (2026-03-06)

### ⚠ BREAKING CHANGES

- replace just, direnv, and scripts/ with mise as
  the single tool manager, task runner, and env loader.

* justfile removed; all tasks now in mise.toml or .mise-tasks/
* .envrc/.envrc.template removed; env vars declared in mise.toml [env]
* scripts/ migrated to file-based mise tasks in .mise-tasks/
* Complex tasks (scaffold, upversion, registry-login, publish,

### Features

- migrate from justfile/direnv/scripts to mise

BREAKING CHANGE: replace just, direnv, and scripts/ with mise as
the single tool manager, task runner, and env loader.

- justfile removed; all tasks now in mise.toml or .mise-tasks/
- .envrc/.envrc.template removed; env vars declared in mise.toml [env]
- scripts/ migrated to file-based mise tasks in .mise-tasks/
- Complex tasks (scaffold, upversion, registry-login, publish,
  upgrade) are executable scripts with #MISE description pragmas
- Internal helpers (utils, toggle-files) hidden with #MISE hide=true
- Claude plugin install moved from Dockerfile to postCreateCommand
- CI workflows updated to use mise-action and mise run throughout

### Bug Fixes

- exclude node_modules from test rsync and git archive

Local npm install creates node_modules/ in the project root, causing
test setup rsync to copy it into temp clones (slowing tests) and
triggering vanished-file warnings when scaffold ran concurrently.

- install npm deps to project root with --prefix

Without a package.json in the project directory, npm install walked up
and installed into $HOME. Adding --prefix "{{config_root}}" forces
install into the project root so node*modules/.bin is on PATH via the
*.path mise env entry.

- install semantic-release plugins via mise npm tools

Replaces the deleted setup.sh npm install step. All plugins
declared in .releaserc.json are now managed by mise alongside
other project tools.

- use mise-installed semantic-release instead of npx

npx downloads an isolated copy that cannot find the plugins
installed by mise. Calling semantic-release directly uses the
mise-managed binary which shares the global npm prefix with all
declared npm: tools.

## [1.16.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.15.0...v1.16.0) (2025-11-17)

### Features

- add template commands and cleanup for plugin migration

- complete plugin migration cleanup and scaffolding updates

Updates template to work with Claudevoyant plugin by removing
migrated command references, configuring scaffold to copy only
/upgrade to client repos, and updating all documentation to
reflect the plugin-based workflow.

## [1.15.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.14.2...v1.15.0) (2025-11-16)

### Features

- migrate slash commands to Claudevoyant plugin

Moves all slash commands (/plan, /commit, /upgrade, etc.) from template
files to the Claudevoyant plugin for independent versioning and updates.

- Remove command .md files from .claude/commands/ (now in plugin)
- Add automatic plugin installation to setup.sh --dev
- Update documentation to reference plugin installation
- Update tests to verify commands are excluded from template exports

Users benefit from independently updatable commands without requiring
template upgrades.

## [1.14.2](https://github.com/cloudvoyant/mise-lib-template/compare/v1.14.1...v1.14.2) (2025-11-15)

### Bug Fixes

- use action-gh-release for proper glob pattern support

Replace gh CLI with softprops/action-gh-release@v1 to properly
handle dist/\*_/_ glob patterns when creating GitHub releases.

## [1.14.1](https://github.com/cloudvoyant/mise-lib-template/compare/v1.14.0...v1.14.1) (2025-11-15)

### Bug Fixes

- add build step before creating GitHub release

Add build-prod step to generate dist artifacts before release
creation to prevent "no matches found for dist/\*_/_" error.

## [1.14.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.13.0...v1.14.0) (2025-11-15)

### Features

- improve CI performance and setup script organization

* Add dependency caching to CI and release workflows (binaries, npm, apt)
* Refactor setup.sh with modular flags (--dev, --ci, --template, --docker-optimize)
* Add template documentation files for scaffolded projects
* Improve Docker image optimization support

### Bug Fixes

- align workflow cache paths and CI dependencies with template

* Update binary cache path to /usr/local/bin for consistency
* Add bats-core and parallel installation to --ci flag
* Update setup.sh documentation to reflect CI dependencies

- correct secret checks in release workflow conditionals

Replace invalid secrets.GCP_SA_KEY references with env.GCP_SA_KEY
to fix GitHub Actions validation errors. GitHub Actions does not
allow direct secret access in if conditions.

## [1.13.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.12.0...v1.13.0) (2025-11-02)

### Features

- add dependency caching to CI and release workflows ([8529dcc](https://github.com/cloudvoyant/mise-lib-template/commit/8529dcc74ea69ab132f93de3ee122ecfefd16c2a))

## [1.12.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.11.0...v1.12.0) (2025-11-02)

### Features

- add VS Code settings for team consistency ([7b4e8b8](https://github.com/cloudvoyant/mise-lib-template/commit/7b4e8b8600e79662a87aa3fd9bf08e8efed542e6))

## [1.11.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.10.3...v1.11.0) (2025-11-02)

### Features

- rename platform variables to template for clarity ([c86050f](https://github.com/cloudvoyant/mise-lib-template/commit/c86050f4075719c3685413a5646d1f7debc749ea))

## [1.10.3](https://github.com/cloudvoyant/mise-lib-template/compare/v1.10.2...v1.10.3) (2025-11-01)

### Performance Improvements

- optimize CI by removing unnecessary dependencies ([c242fe2](https://github.com/cloudvoyant/mise-lib-template/commit/c242fe200272f6acc00d6cc51c6509fbfaff9c15))

## [1.10.2](https://github.com/cloudvoyant/mise-lib-template/compare/v1.10.1...v1.10.2) (2025-11-01)

### Bug Fixes

- consolidate CI workflow into single build-and-test job ([6e8e776](https://github.com/cloudvoyant/mise-lib-template/commit/6e8e776af61a5d50a3b7bdaabb6e08743228bfaa))

## [1.10.1](https://github.com/cloudvoyant/mise-lib-template/compare/v1.10.0...v1.10.1) (2025-11-01)

### Bug Fixes

- keep .claude directory visible and document search limitation ([815aabd](https://github.com/cloudvoyant/mise-lib-template/commit/815aabd490cecd54c2d00c242fc1abc71e3c89b4))

## [1.10.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.9.1...v1.10.0) (2025-11-01)

### Features

- add Docker and docker-compose support with multi-stage builds ([46c7aed](https://github.com/cloudvoyant/mise-lib-template/commit/46c7aedd5b1076c50f3c584ac6b8a0464452c62e))
- add VS Code file visibility toggle commands ([0146f50](https://github.com/cloudvoyant/mise-lib-template/commit/0146f504c0d05606e97a4d79653feec46b713d1f))
- enhance changelog generation and publishing ([46c796a](https://github.com/cloudvoyant/mise-lib-template/commit/46c796a30452cb1cb2d8389b10fc3d0489b5440c))

### Bug Fixes

- add missing conventionalcommits changelog dependency ([a0f2841](https://github.com/cloudvoyant/mise-lib-template/commit/a0f28414afa9775b83e1ad9cd723542959433077))

### Documentation

- add section on viewing hidden files in VS Code ([9dfc104](https://github.com/cloudvoyant/mise-lib-template/commit/9dfc104aa247c2727eacab0ca7af7e454707eb9b))
- improve architecture.md consistency and add review command ([7426f30](https://github.com/cloudvoyant/mise-lib-template/commit/7426f3035ca10b961380244fc190bb53fd497597))
- improve commit command workflow instructions ([069bea7](https://github.com/cloudvoyant/mise-lib-template/commit/069bea7ceafddc96259cdd32927d587b147c6649))
- improve markdown formatting and user guide clarity ([ab4d313](https://github.com/cloudvoyant/mise-lib-template/commit/ab4d313f01ddd60e889e9719a8fcd654c8ae9e0d))
- refactor template placeholders and fix ADR references ([f1ab275](https://github.com/cloudvoyant/mise-lib-template/commit/f1ab275cdf7a0e62994ece0761494252f6922be9))

## [1.9.1](https://github.com/cloudvoyant/mise-lib-template/compare/v1.9.0...v1.9.1) (2025-10-15)

### Bug Fixes

- use get_version() in upversion.sh to properly detect new releases ([a145bc2](https://github.com/cloudvoyant/mise-lib-template/commit/a145bc270bbc01c75100d927ff5aa210caf33842))

# [1.9.0](https://github.com/cloudvoyant/mise-lib-template/compare/v1.8.2...v1.9.0) (2025-10-14)

### Bug Fixes

- getting version from tags to keep single source of truth, and avoid need for drenv reloads due to .,envrc changes on commits ([6c0004a](https://github.com/cloudvoyant/mise-lib-template/commit/6c0004a58dc66d95fcfa5b2eb45ba86d8a587a94))

### Features

- use version.txt as single source of truth for versioning ([956fe0d](https://github.com/cloudvoyant/mise-lib-template/commit/956fe0d7bab5e14e377ead1de652d31148ed11ee))

## [1.8.2](https://github.com/cloudvoyant/mise-lib-template/compare/v1.8.1...v1.8.2) (2025-10-14)

### Bug Fixes

- readme badhes ([c2990cb](https://github.com/cloudvoyant/mise-lib-template/commit/c2990cbccd56accccc95ebcf92eec1c102d05947))

## [1.8.1](https://github.com/cloudvoyant/lib/compare/v1.8.0...v1.8.1) (2025-10-13)

### Bug Fixes

- remove RELEASE_NOTES.md reference from release workflow ([07c9912](https://github.com/cloudvoyant/lib/commit/07c9912d7e72114efeaf98fa2653ce333805a166))

# [1.8.0](https://github.com/cloudvoyant/lib/compare/v1.7.0...v1.8.0) (2025-10-13)

### Features

- docs added. ([508a1d5](https://github.com/cloudvoyant/lib/commit/508a1d5ff02a48017330a99221ef2a6632516d1a))

# [1.7.0](https://github.com/cloudvoyant/lib/compare/v1.6.6...v1.7.0) (2025-10-12)

### Features

- semantic setup flags, versioning script, and simplified config ([716a0e7](https://github.com/cloudvoyant/lib/commit/716a0e7bbf374ffa7e8223773e7504bbc96a0582))

## [1.6.6](https://github.com/cloudvoyant/lib/compare/v1.6.5...v1.6.6) (2025-10-12)

### Bug Fixes

- source .envrc in publish recipe for CI compatibility ([dee4d82](https://github.com/cloudvoyant/lib/commit/dee4d821f307c7ff2803848fdd516b73a1eed9a2))

## [1.6.5](https://github.com/cloudvoyant/lib/compare/v1.6.4...v1.6.5) (2025-10-12)

### Bug Fixes

- cleaned up justfile ([281c6fc](https://github.com/cloudvoyant/lib/commit/281c6fce7801b9cc90f53bdb192901750268ad67))

## [1.6.4](https://github.com/cloudvoyant/lib/compare/v1.6.3...v1.6.4) (2025-10-12)

### Bug Fixes

- cleaned up justfile ([64def9c](https://github.com/cloudvoyant/lib/commit/64def9c96034e3c01c70e01ef1e58ecae0b04f99))

## [1.6.3](https://github.com/cloudvoyant/lib/compare/v1.6.2...v1.6.3) (2025-10-12)

### Bug Fixes

- attempting to fix issues with publish ([8732a18](https://github.com/cloudvoyant/lib/commit/8732a189610182c00f6c66c423c30bf402e4b2ec))
- attempting to fix issues with publish ([3642966](https://github.com/cloudvoyant/lib/commit/3642966677a9c607e9cc56dea538bae3416e700f))

## [1.6.2](https://github.com/cloudvoyant/lib/compare/v1.6.1...v1.6.2) (2025-10-12)

### Bug Fixes

- adding logging to publish task ([02c1451](https://github.com/cloudvoyant/lib/commit/02c14514bfab8205b50afc047b55c5a9cd826362))

## [1.6.1](https://github.com/cloudvoyant/lib/compare/v1.6.0...v1.6.1) (2025-10-12)

### Bug Fixes

- adding logging to publish task ([0ec390b](https://github.com/cloudvoyant/lib/commit/0ec390bcde8ab6c6ac349e96efb7dfba19523c8e))

# [1.6.0](https://github.com/cloudvoyant/lib/compare/v1.5.0...v1.6.0) (2025-10-12)

### Features

- doc updates ([49ce340](https://github.com/cloudvoyant/lib/commit/49ce340a022fece8c2f04598305c018d4e1aa061))

# [1.5.0](https://github.com/cloudvoyant/lib/compare/v1.4.1...v1.5.0) (2025-10-12)

### Features

- doc updates ([11e75ad](https://github.com/cloudvoyant/lib/commit/11e75ad451e48a0200b77b42c611a54e8984965f))

## [1.4.1](https://github.com/cloudvoyant/lib/compare/v1.4.0...v1.4.1) (2025-10-12)

### Bug Fixes

- use shebang for registry-login recipe to fix bash syntax error ([774ab53](https://github.com/cloudvoyant/lib/commit/774ab53fc6ff3e02373b54d9c343ca5ca1065d62))

# [1.4.0](https://github.com/cloudvoyant/lib/compare/v1.3.0...v1.4.0) (2025-10-12)

### Features

- doc updates ([c90dfd2](https://github.com/cloudvoyant/lib/commit/c90dfd201e2b6557e50a368f2e198fa8ece9d426))

# [1.3.0](https://github.com/cloudvoyant/lib/compare/v1.2.0...v1.3.0) (2025-10-12)

### Features

- consolidate setup, generalize versioning, and separate registry config ([d17cbfb](https://github.com/cloudvoyant/lib/commit/d17cbfbfda8ede27bb3235a9743a044014f40308))
- replace just release-notes with Claude command ([d34ba73](https://github.com/cloudvoyant/lib/commit/d34ba737a848278f6c753111a4add7e6afc3ed78))

# [1.2.0](https://github.com/cloudvoyant/lib/compare/v1.1.0...v1.2.0) (2025-10-11)

### Features

- consolidate tests and add ADR management commands ([c60041e](https://github.com/cloudvoyant/lib/commit/c60041ed5077d3da573dc7a9b33ededaebc863e7))
- enhance validate-docs and add development guidelines ([0d04656](https://github.com/cloudvoyant/lib/commit/0d046560c7c025339df669326c1fea668e310e87))
- make validate-docs command general-purpose ([e7056fa](https://github.com/cloudvoyant/lib/commit/e7056fa8aa3edcacad99c8a53d56c1f6f8532495))

# [1.1.0](https://github.com/cloudvoyant/lib/compare/v1.0.4...v1.1.0) (2025-10-09)

### Bug Fixes

- added error handling in scaffold.sh ([07cb982](https://github.com/cloudvoyant/lib/commit/07cb982c77b43ca91bee52688977797784f97ce0))
- added error handling in scaffold.sh ([99f4ad2](https://github.com/cloudvoyant/lib/commit/99f4ad2202bdea5291dc22701e05365919454461))
- added scripts to install platform deps ([84389ed](https://github.com/cloudvoyant/lib/commit/84389ed459c8cc7874bf3f301f9741e22933d37a))

### Features

- add GitHub template support and migration workflows ([0325a6f](https://github.com/cloudvoyant/lib/commit/0325a6faf6d76a9f72c84419c365cdb956c3e338))
- enhanced string replacement in scaffold.sh ([bce0569](https://github.com/cloudvoyant/lib/commit/bce056919d9a2eadf663839c1d2e99a8331185c4))

## [1.0.4](https://github.com/cloudvoyant/lib/compare/v1.0.3...v1.0.4) (2025-10-06)

### Bug Fixes

- getting rid of redundant VERSION file ([c5a1e47](https://github.com/cloudvoyant/lib/commit/c5a1e4740fedd6420bf42f3b00b2631d5eb6eaeb))

## [1.0.3](https://github.com/cloudvoyant/lib/compare/v1.0.2...v1.0.3) (2025-10-06)

### Bug Fixes

- fixed issues with release note gen script ([d399312](https://github.com/cloudvoyant/lib/commit/d399312ab5d6f53f90b6ebe6fd906b17824aae65))

## [1.0.2](https://github.com/cloudvoyant/lib/compare/v1.0.1...v1.0.2) (2025-10-06)

### Bug Fixes

- resolving github action not publishing by using available action ([2fa1e94](https://github.com/cloudvoyant/lib/commit/2fa1e94b207284a661a11afc1d620fbe393ad111))

## [1.0.1](https://github.com/cloudvoyant/lib/compare/v1.0.0...v1.0.1) (2025-10-06)

### Bug Fixes

- publishing during release pipeline to work around ci-skip not triggerring tag pipeline ([33d0f89](https://github.com/cloudvoyant/lib/commit/33d0f8925e152ffdca540bcf474846e424bb85d1))
- publishing during release pipeline to work around ci-skip not triggerring tag pipeline ([1a9f2a9](https://github.com/cloudvoyant/lib/commit/1a9f2a9e2b21c7d2f6b7c14a3e3d5080cfe4869d))

# 1.0.0 (2025-10-04)

### Bug Fixes

- resolved semantic release configuration bug ([a65863a](https://github.com/cloudvoyant/lib/commit/a65863aa4ef78054d8c8f8161d0431243613c8a6))

### Features

- scaffolded project structure, added standard scripts and justfile, .envrc and claude config files ([d4d60d1](https://github.com/cloudvoyant/lib/commit/d4d60d1b1b93838c03902a4da2010ee79921b560))
