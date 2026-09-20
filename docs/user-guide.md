# User Guide

premise creates applications and libraries from live templates and records where each generated project came from. Templates remain valid projects before generation because premise uses literal string replacement instead of template-expression syntax.

## Requirements

- Install premise with `install.sh`; the installer adds both `premise` and its `pm` alias. `go install` adds only `premise`.
- Install mise and trust the repository configuration.
- Use a terminal for interactive questionnaires.
- Allow network access when premise fetches a remote template repository for the first time.

## Getting Started

Initialize a monorepo and generate a project from an official registry:

```bash
pm init
pm generate :premise-app
```

Initialize a separate template registry when you author templates:

```bash
pm init --kind template-registry
pm template init app
pm template init lib
pm template ls
pm template test
```

A Premise root is either a monorepo or a template registry. It cannot be both.

## Usage

### Initialize a workspace

Run `pm init` at the repository root. The default `monorepo` kind creates `premise.yaml`, a root `mise.toml`, `apps/`, and `libs/`. The root Mise configuration discovers app and library projects and layers their tools and environment. Use `pm init --kind template-registry` to create a registry manifest and `templates/` directory without monorepo conventions. Premise preserves existing files and refuses to replace an existing manifest.

Use `pm install` or `pm i` to install mise tools declared by the workspace and its generated projects. It does not install package-manager dependencies yet. Run project lifecycle tasks directly through mise's monorepo pattern:

```bash
mise run --jobs 1 '//...:build'
mise run --jobs 1 '//...:test'
```

### Initialize and list templates

Run these commands in a project initialized with `--kind template-registry`:

```bash
pm template init app
pm template init lib
pm template ls
```

Each init command creates `templates/<kind>/mise.toml` and adds a matching declaration to `premise.yaml`. Omit the kind to select it interactively. `pm template ls` prints declared template names in stable alphabetical order. Premise rejects template initialization in a monorepo.

Files placed directly under `templates/` are shared scaffold files. Premise compares these files with the selected `templates/<name>/` tree after it applies questionnaire substitutions. Directories under `templates/` are template sources and are not copied as shared content.

Premise prints one path-sorted conflict preview before it creates the destination. Root collisions use three tiers:

| Tier                                      | Merge behavior                                                                    |
| ----------------------------------------- | --------------------------------------------------------------------------------- |
| Tier 1: `mise.toml`                       | Parse typed Mise data and apply semantic rules for tools and environment values.  |
| Tier 2: `.gitignore` and `.gitattributes` | Keep shared lines first and selected lines second because line order has meaning. |
| Tier 3: any other differing root file     | Keep the complete shared file, use the complete selected file, or abort.          |

Equal files need no decision. Premise does not parse editor configuration, Prettier files, package manifests, or arbitrary ignore files as smart merge formats.

When contract tasks collide, Premise keeps the selected task metadata and appends shared commands before selected commands. Root contract tasks are expected to be argument-free. When a non-contract task collides, the shared task keeps its name and the selected task is copied to `<registry-prefix>:<task>`. Premise prints a notice naming that namespace. Only the contract task command sequence is combined; other task fields remain one-sided.

The initial Mise tasks echo their contract names. Replace each echo with the real implementation while keeping the task name stable.

### Test templates

```bash
pm template test
```

Premise enters each declared template directory, installs its Mise tools, and executes every required task. It continues after failures and returns one combined error containing every missing or failing contract.

### Run CI flows

```bash
pm ci flow on-commit
pm ci flow on-merge
pm ci flow on-release --environment stage
```

A root `on-commit`, `on-merge`, or `on-release` Mise task overrides the fallback lifecycle. The `pm ci flow` command still owns publication after that lifecycle. Template registries run fallback lifecycle tasks inside each template. App-only deploy and end-to-end tasks do not run for libraries. `on-commit` invokes `publish:rc` only for a non-main branch push whose HEAD contains `[publish-rc]`; pull requests never run it. `on-merge` invokes the stable release phase after validation.

### Generate from a local registry

```bash
pm generate ../my-registry:app
pm generate ../my-registry:lib
```

Premise asks the selected template's questions and creates `apps/<name>` or `libs/<name>` according to its kind. It shows all root collisions and resolves each conflict before it writes the destination. Premise records the project, source-qualified template selector, path, answers, and declared template version under `workspace.projects`.

Before materializing the full merge, Premise runs a dependency preflight for each selected-template tool version that the shared `mise.toml` would change. Each preflight uses a disposable copy, `mise install`, and every required template contract. Premise then materializes the merge privately and runs final validation in another disposable copy with `mise install` and every required contract under `PREMISE_TEMPLATE_TEST=1`. Only a completely validated candidate is renamed into the destination; a failed decision, preflight, or final validation leaves the destination and `premise.yaml` unchanged.

### Choose from the default registry

```bash
pm generate
```

When the selector is omitted, Premise loads every official registry — `cloudvoyant/premise`, `cloudvoyant/premise-cargo`, and `cloudvoyant/premise-bun` — and lists their templates in one interactive picker. The combined entries are randomized for each prompt so no registry or language keeps the first position. The Bun entries are `premise-commander-cli`, `premise-hono-api`, `premise-opentui-cli`, `premise-sveltekit-app`, and `premise-tanstack-start-app`. Choosing an entry records its fully qualified `<owner>/<repo>:<template>` selector, so a Bun template resolves to `cloudvoyant/premise-bun:<template>` and never collides with a same-named template from another source.

### Generate from one registry

```bash
pm generate cloudvoyant/premise-cargo
pm generate cloudvoyant/premise-bun
```

Passing an `<owner>/<repo>` with no `:<template>` loads that one registry and opens a picker scoped to its templates, returning the selected entry's fully qualified selector. This is the same picker as `pm generate` with no argument, narrowed to a single source.

### Generate a bare template name

```bash
pm generate :premise-rust-lib
pm generate :premise-hono-api
```

The leading-colon shorthand names a template without a source. Premise resolves it against every official registry and generates the single match, so `:premise-rust-lib` resolves to Cargo and `:premise-hono-api` resolves to Bun. If the name matches no official registry, or matches more than one, Premise reports the ambiguity and asks you to qualify the source. A bare name is never searched across unofficial registries; reach those with a fully qualified `<owner>/<repo>:<template>` selector.

### Generate from a remote repository

```bash
pm generate cloudvoyant/premise-template:app
```

The remote repository must contain `premise.yaml` and `templates/<template>` for the selected manifest entry. Premise clones it through go-git and caches it below `~/.premise/templates`; later runs refresh that cache. Selectors accept `owner/repository:<template>`, HTTPS Git URLs such as `https://github.com/cloudvoyant/premise-template.git:app`, and SSH Git URLs followed by `:<template>`.

Remote selectors follow the repository's default branch and do not accept a branch, tag, or commit. During registry development, use an absolute local selector such as `/path/to/premise-bun:premise-hono-api` to test an unmerged branch.

### Manifest

```yaml
workspace:
  name: example
  kind: monorepo
  schema-version: "0.1"
  providers:
    ci: github
    tools: mise
    tasks: mise
    infra: pulumi
    versioning: svu
  projects: []
templates:
  - name: premise-app
    kind: app
    version: 0.1.0
    questions:
      - prompt: "App name:"
        type: string
        populate: name
    substitutions:
      premise-app: name
```

Each key under `substitutions` is literal text that exists in the live template. Its value names the questionnaire answer that replaces it. In this example, generation replaces every `premise-app` occurrence in UTF-8 text files with the app name; binary files remain unchanged.

Question type `string` accepts free text. Types `radio` and `select` require a non-empty `choices` list:

```yaml
questions:
  - prompt: "Runtime:"
    type: select
    populate: runtime
    choices:
      - node
      - go
```

After generation, Premise records provenance under `workspace.projects`:

```yaml
projects:
  - name: orders
    template: .:app
    version: 0.1.0
    path: apps/orders
    answers:
      name: orders
```

`version` records the selected template declaration's version at generation time when the registry provides one; registries with repository-level versioning can omit it. If project registration or manifest saving fails after the validated stage is renamed, Premise removes the newly created destination so output and provenance do not diverge. Generation is create-only; crash-consistent updates across the destination and manifest are out of scope.

### Task contracts

Applications and libraries must provide these tasks:

```text
install
build
clean
test
lint
lint:fix
format
format:check
env-pull
publish:rc
publish
```

Applications must also provide:

```text
run
dev
deploy
e2e
```

Templates can define additional tasks.

### Versioning

Premise calculates release versions through the svu Go SDK, exposed by the `pm version` command:

```bash
pm version current                  # current stable version (e.g. v0.1.0)
pm version next                     # next version from git history
pm version bump patch|minor|major   # explicit patch/minor/major bump
pm version rc --identifier <id>     # MAJOR.MINOR.PATCH-rc.<id>
```

Each command prints exactly one version to stdout. Release-candidate identifiers must be valid SemVer prerelease identifiers: letters, digits, and hyphens, with numeric identifiers forbidding leading zeroes.

Version calculation relies on a `v0.0.0` stable bootstrap tag that must exist before CI runs. That tag is created externally and is never produced by a task or workflow. Premise configures the SDK to read only stable SemVer tags (`vMAJOR.MINOR.PATCH`), so unrelated tags are ignored. No `.svu.yml` file or svu executable is required.

### Publishing

Stable releases happen on pushes to `main`. The workflow calls `pm ci flow on-merge`, which validates the trunk and then invokes the stable release phase. Premise reuses a stable tag already present at HEAD or computes, creates, and pushes the next `vMAJOR.MINOR.PATCH` tag. It generates temporary GoReleaser configuration and publishes the GitHub archives that `install.sh` downloads. If there is no release-worthy change, the command skips cleanly. Reruns reuse the tag and replace conflicting release assets. Repositories do not carry `.goreleaser.yml`.

Registries that publish language packages can keep credentials in separate CI steps with `pm release prepare`, `pm release github`, and `pm release packages`. `pm release snapshot` builds the complete artifact matrix without tagging or publishing.

Release-candidate publication is opt-in for Go: a feature-branch push whose HEAD commit message contains the exact marker `[publish-rc]` runs `mise run publish:rc`, which succeeds and prints only `Skipping RC publish: Go supports prerelease installs through commit hashes.` Go needs no prerelease artifact because installs resolve through commit hashes, so no RC tag or release is ever created.

### Current limitations

- Template source paths are fixed by template name, and generated roots are fixed by app/lib kind.
- Substitution changes UTF-8 file contents but not file or directory names.
- Remote templates use the repository's default branch.
- Private-repository authentication, concurrent cache locking, and offline mode are not available yet. Credential delegation for private registry sources is deferred to DIFF-150; go-git performs clones today.
- Premise refuses to merge into or replace an existing destination.
- Smart root merging is limited to `.gitignore`, `.gitattributes`, and `mise.toml`. Other root collisions use a complete-file choice.
- Generation is create-only. It does not update existing projects or provide crash recovery across the destination and workspace manifest.
