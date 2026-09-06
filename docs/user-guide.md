# User Guide

`mise-lib-template` is a language-agnostic [`mise`](https://mise.jdx.dev/)-powered [1] template for building library/executable projects. It uses GCP Artifact Registry for publishing generic packages by default, but can be easily adapted for npm, PyPI, NuGet, CodeArtifact, etc.

## Features

Here's what this template gives you off the bat:

- A language-agnostic self-documenting command interface via `mise` — keep all your project tasks, tool versions, and environment config in one `mise.toml`!
- Cross-platform environment management - mise installs any dev-tools your project defines
- CI/CD with GitHub Actions - run test on MR commits, tag and release on merges to main.
- Easy CI/CD customization - simply modify mise tasks that hook into actions
- Trunk-based development and automated versioning with conventional commits - just on feature branches and merge to main for bumps, semantic-release will handle version bumping for you!
- GCP Artifact Registry publishing (easily modified for other registries)

## Requirements

- bash 3.2+
- [mise](https://mise.jdx.dev/) — manages tools, tasks, and environment

To install mise, run:

```bash
❯ curl https://mise.jdx.dev/install.sh | sh
```

## Choosing a Template

| Template | When to use                                      | Language                 | Registry                 |
| -------- | ------------------------------------------------ | ------------------------ | ------------------------ |
| agnostic | Any language — fill in your own tasks            | Any                      | GCP Artifact Registry    |
| uv       | Python library or CLI                            | Python 3.12+             | PyPI                     |
| go       | Go library or CLI with cross-platform builds     | Go 1.24+                 | GitHub Releases          |
| rust     | Rust library/binary; git-install + binaries      | Rust (stable)            | GitHub Releases          |
| odin     | Odin source library (distributed as source)      | Odin (github backend)    | GitHub Releases (source) |
| zig      | Zig library or binary with cross-platform builds | Zig 0.16.x               | GitHub Releases          |
| pnpm     | TypeScript library published to npm              | TypeScript / Node.js LTS | npm                      |

All tools are installed automatically by mise — you do not need to install any language toolchain separately.

## Quick Start

Scaffold a new project:

```bash
# Click "Use this template" on GitHub, then:
❯ git clone <your-new-repo>
❯ cd <your-new-repo>
❯ bash mise-tasks/scaffold --project your-project-name --template uv    # Python
❯ bash mise-tasks/scaffold --project your-project-name --template go    # Go
❯ bash mise-tasks/scaffold --project your-project-name --template rust  # Rust
❯ bash mise-tasks/scaffold --project your-project-name --template odin  # Odin
❯ bash mise-tasks/scaffold --project your-project-name --template zig   # Zig
❯ bash mise-tasks/scaffold --project your-project-name                   # agnostic (prompted)
```

For compiled languages whose package paths embed an org (e.g. Go's module
path), pass `--github-org <org>` so the module resolves to
`github.com/<org>/<project>` instead of a duplicated name. It defaults to the
git remote's org, else a placeholder you can edit:

```bash
❯ bash mise-tasks/scaffold --project your-project-name --template go --github-org your-org
```

Install dependencies and scaffold the template for your needs:

```bash
# Install project tools
❯ mise install

# Scaffold project for your language
❯ mise run scaffold
```

Type `mise tasks` to see all the tasks at your disposal:

```bash
❯ mise tasks
```

Build, run and test with `mise run`. The template will show TODO messages in console prior to adapting.

```bash
❯ mise run run
TODO: Implement build for mise-lib-template@2.x
TODO: Implement run

❯ mise run test
TODO: Implement build for mise-lib-template@2.x
TODO: Implement test
```

Mise runs the necessary task dependencies automatically!

Commit using conventional commits (`feat:`, `fix:`, `docs:`). Merge/push to main and CI/CD will run automatically bumping your project version and publishing a package.

## Template-Specific Tasks

Regardless of which template you chose, the same `mise run` commands work identically:

```bash
mise run build         # build your project
mise run test          # run tests
mise run lint          # run static analysis
mise run format        # format code
mise run publish       # publish to your registry
```

`mise run install` installs the correct toolchain for your template (Python + uv, Go, Rust, Odin, Zig, or Node for semantic-release).

## The Basics

### Development

```bash
mise run install    # Install project dependencies
mise run build      # Build for development
mise run test       # Run tests
mise run run        # Run locally
mise run clean      # Clean build artifacts
```

### Commit and Release

Use conventional commits for automatic versioning:

```bash
git commit -m "feat: add new feature"      # Minor bump (0.1.0 → 0.2.0)
git commit -m "fix: resolve bug"           # Patch bump (0.1.0 → 0.1.1)
git commit -m "docs: update readme"        # No bump
git commit -m "feat!: breaking change"     # Major bump (0.1.0 → 1.0.0)
```

Push to main:

```bash
git push origin main
```

CI/CD automatically runs tests, creates a release, and publishes to your configured registry.

### Versioning starts at v0

New projects start their version history in the `0.x` range. The first release is computed from a
seeded `v0.0.0` baseline: a `feat:` commit yields `0.1.0`, a `fix:` yields `0.0.1`. The project stays
in `0.x` until you intentionally cut `1.0.0`. Note: the first breaking change (`feat!:` /
`BREAKING CHANGE:`) below 1.0.0 promotes the project straight to `1.0.0` (a semantic-release behaviour).

## Customizing The Template For Your Needs

### For Your Language

The `mise.toml` tasks contain TODO placeholders. Run Claude's `/adapt` command for guided customization:

```bash
claude /adapt
```

Or manually replace placeholders with your language's commands:

```toml
# Node.js example
[tasks.install]
run = "npm install"

[tasks.build]
run = "npm run build"

[tasks.test]
depends = ["build"]
run = "npm test"

[tasks.publish]
depends = ["test", "build:prod"]
run = "npm publish"
```

### For Your Registry

The `publish` task defaults to GCP Artifact Registry. Edit it in `mise.toml` for your registry:

```toml
# npm
[tasks.publish]
depends = ["test", "build:prod"]
run = "npm publish"

# PyPI
[tasks.publish]
depends = ["test", "build:prod"]
run = "twine upload dist/*"
```

Configure your `mise.toml` `[env]` section accordingly:

```toml
# GCP (default)
GCP_REGISTRY_PROJECT_ID = "my-project"
GCP_REGISTRY_REGION     = "us-east1"
GCP_REGISTRY_NAME       = "my-registry"

# Or use registry-specific variables for npm, PyPI, etc.
```

### CI/CD Secrets

Configure secrets once at the organization level (Settings → Secrets → Actions):

For GCP (agnostic template, default):

- `GCP_SA_KEY` - Service account JSON key
- `GCP_REGISTRY_PROJECT_ID`, `GCP_REGISTRY_REGION`, `GCP_REGISTRY_NAME`

For PyPI (uv template):

- `UV_PUBLISH_TOKEN` — PyPI API token (or configure [OIDC trusted publishing](https://docs.pypi.org/trusted-publishers/))

For GitHub Releases (go, rust, odin, zig templates):

- `GH_TOKEN` or `GITHUB_TOKEN` — already present via GitHub Actions default token

  The `odin` template distributes **source** (git tag / GitHub source release),
  not binaries; go, rust, and zig attach cross-platform binaries to the release.

For npm (pnpm template):

- `NPM_TOKEN` — npm access token with publish rights (see [npm Setup](#npm-setup) below)

All projects automatically inherit organization secrets.

### npm Setup

The `pnpm` template publishes to [npm](https://www.npmjs.com/). Follow these steps to configure publishing for your project and CI/CD.

#### 1. Create an npm Account

Sign up at [npmjs.com](https://www.npmjs.com/signup) if you don't have one.

#### 2. Create an npm Organization (optional, for scoped packages)

Scoped packages (`@your-org/package-name`) require an npm organization:

1. Go to [npmjs.com/org/create](https://www.npmjs.com/org/create)
2. Choose a name (this becomes your scope, e.g. `@your-org`)
3. Free orgs can publish public scoped packages

To use a scoped name, update `"name"` in your project's `package.json`:

```json
{
  "name": "@your-org/my-library"
}
```

#### 3. Create an npm Access Token

1. Log in to [npmjs.com](https://www.npmjs.com/)
2. Click your avatar → **Access Tokens** → **Generate New Token**
3. Choose **Granular Access Token** (recommended) or **Classic Token → Automation**
   - **Granular**: set expiry, select the specific packages to allow publishing, set permission to **Read and write**
   - **Classic Automation**: no expiry, grants publish to all packages in the account — simpler but broader scope
4. Copy the token — it is only shown once

#### 4. Add `NPM_TOKEN` to GitHub Secrets

**Per-repository** (for a single project):

1. Go to your repository → **Settings** → **Secrets and variables** → **Actions**
2. Click **New repository secret**
3. Name: `NPM_TOKEN`, Value: paste your token
4. Click **Add secret**

**Organization-level** (shared across all repos — recommended):

1. Go to your GitHub organization → **Settings** → **Secrets and variables** → **Actions**
2. Click **New organization secret**
3. Name: `NPM_TOKEN`, Value: paste your token
4. Set **Repository access** to **All repositories** (or select specific ones)
5. Click **Add secret**

All repositories in the org automatically inherit organization-level secrets. This means every project using the `pnpm` template will be able to publish without configuring secrets per-repo.

#### 5. Token Expiration

**npm Granular Access Tokens expire after 90 days by default.** Set a calendar reminder to rotate before expiry, or use Trusted Publishing (below) to eliminate tokens entirely.

Classic Automation tokens can be set to no expiry — simpler but grants broader access.

#### 6. Trusted Publishing (Recommended — no token rotation)

npm supports OIDC-based trusted publishing, letting GitHub Actions publish without storing any token. Set it up once per package after the first manual publish.

**Setup (on npmjs.com):**

1. Publish the package at least once manually first (npm requires the package to exist)
2. Go to the package page → **Settings** → **Publishing access** → **Require two-factor authentication or automation token** → switch to **Allow publishing from CI/CD with a generated token**
3. Set:
   - **Repository owner**: your GitHub username or org
   - **Repository name**: your repo name
   - **Workflow**: `release.yml`

**Update `release.yml`** to use OIDC instead of `NPM_TOKEN`:

```yaml
permissions:
  contents: write
  issues: write
  pull-requests: write
  id-token: write # ← add this for OIDC

# In the Publish package step, remove NPM_TOKEN from env.
# pnpm publish will authenticate via the OIDC token automatically.
```

Also add `--provenance` to the publish command in `mise.toml`:

```toml
[tasks.publish]
run = "pnpm publish --access public --no-git-checks --provenance"
```

Once trusted publishing is configured, remove `NPM_TOKEN` from GitHub secrets — it is no longer needed.

#### 7. Verify

After setting the secret (or configuring trusted publishing), push a `feat:` or `fix:` commit to `main`. CI will:

1. Run `mise run upversion` — bumps version in `package.json`, creates git tag
2. Run `mise run publish` — calls `pnpm publish --access public --no-git-checks`

Check the Actions tab to confirm both steps succeed.

#### Troubleshooting

| Error                                  | Cause                                            | Fix                                                                    |
| -------------------------------------- | ------------------------------------------------ | ---------------------------------------------------------------------- |
| `403 Forbidden`                        | Token lacks publish rights or wrong package name | Regenerate token with correct scope; verify `"name"` in `package.json` |
| `402 Payment Required`                 | Scoped package requires paid account or org      | Use a free org, or publish unscoped                                    |
| `ENEEDAUTH`                            | `NPM_TOKEN` secret not set or misspelled         | Check GitHub secrets; ensure secret name is exactly `NPM_TOKEN`        |
| `Cannot publish over existing version` | Version already published                        | Bump version with a new commit; never re-publish the same version      |
| `OIDC token error`                     | Trusted publishing misconfigured                 | Verify repo name, owner, and workflow name match exactly on npmjs.com  |

## Overriding CI/CD

### Customizing Behavior

`mise` tasks provide hooks for overriding CI/CD behavior:

- **build:prod**: specifies how to create production builds in CI
- **test**: specifies hot to test your build
- **publish**: specifies how to publish to your artifact regiistry

Edit these tasks to change how CI/CD runs, but avoid editing `.github/workflows/` directly.

To modify semantic versioning behavior or to deviate fromt trunk-based development, modify your .releaserc.json to modify semantic-versioning CLI's behavior.

## LLM Assistance with Claude

Claude commands provide guided workflows for complex tasks. The template includes two custom commands, while most workflow commands come from the [Claudevoyant plugin](https://github.com/cloudvoyant/claudevoyant) (installed via `mise run install-claude-plugins`).

### Template Commands

```bash
claude /adapt                   # Customize template for your language (auto-deletes after use)
claude /upgrade                 # Migrate to newer template version
```

### Plugin Commands (from Claudevoyant)

```bash
claude /spec:new                # Create a new project plan
claude /spec:go                 # Execute the plan with spec-driven development
claude /dev:docs                # Validate documentation
claude /dev:commit              # Create conventional commit
claude /dev:review              # Perform code review
```

### Upgrading Projects

When a new template version is released:

```bash
claude /upgrade
```

This creates a comprehensive migration plan, compares files, and walks you through changes while preserving your customizations.

## Next Steps

1. Customize `mise.toml` tasks for your language
2. Write code in `src/`
3. Add tests
4. Configure GitHub organization secrets
5. Set up branch protection on `main`
6. Make your first conventional commit
7. Push and watch the automated release

Or just run `claude /adapt`.

See [Architecture](architecture.md) for implementation details.
