# User Guide

premise creates applications and libraries from live templates and records where each generated project came from. Templates remain valid projects before generation because premise uses literal string replacement instead of template-expression syntax.

## Requirements

- Install premise with `install.sh`; the installer adds both `premise` and its `pm` alias. `go install` adds only `premise`.
- Install mise and trust the repository configuration.
- Use a terminal for interactive questionnaires.
- Allow network access when premise fetches a remote template repository for the first time.

## Getting Started

Initialize a workspace and its two conventional template kinds:

```bash
pm init
pm template init app
pm template init lib
pm template test
```

Generate from the current workspace:

```bash
pm generate .:app
pm generate .:lib
```

The short command is equivalent:

```bash
pm g :premise-app
```

## Usage

### Initialize a workspace

Run `pm init` at the repository root. The command creates `premise.yaml`, a root `mise.toml`, `apps/`, and `libs/`. The root mise configuration discovers app and library projects and layers their tools and environment. If `mise.toml` already exists, Premise preserves it. The command refuses to replace an existing manifest. Generation commands return `Not a premise project` until initialization is complete.

Use `pm install` or `pm i` to install mise tools declared by the workspace and its generated projects. It does not install package-manager dependencies yet. Run project lifecycle tasks directly through mise's monorepo pattern:

```bash
mise run --jobs 1 '//...:build'
mise run --jobs 1 '//...:test'
```

### Initialize a template

```bash
pm template init app
pm template init lib
```

Each command creates `templates/<kind>/mise.toml` and adds a matching template declaration to `premise.yaml`. Omit the kind to select it interactively.

The initial mise tasks echo their contract names. Replace each echo with the real implementation while keeping the task name stable.

### Test templates

```bash
pm template test
```

Premise enters each declared template directory and executes every required task through mise. It continues after failures and returns one combined error containing every missing or failing contract.

### Generate from this workspace

```bash
pm generate .:app
pm generate .:lib
```

Premise asks the questions declared by the selected template and creates `apps/<name>` or `libs/<name>` according to its `kind`. It then records the project, template selector, version, path, and answers under `workspace.projects`.

### Choose from the default registry

```bash
pm generate
```

When the selector is omitted, Premise loads every official registry — `cloudvoyant/premise` and `cloudvoyant/premise-cargo` — and lists their templates in one interactive picker: `premise-app` and `premise-lib` (Go), plus `premise-rust-lib`, `premise-rust-app`, `premise-clap-cli`, and `premise-ratatui-app` (Cargo). Choosing an entry records its fully qualified `<owner>/<repo>:<template>` selector, so a picked Cargo template resolves to `cloudvoyant/premise-cargo:<template>` and never collides with a same-named Go template.

### Generate from one registry

```bash
pm generate cloudvoyant/premise-cargo
```

Passing an `<owner>/<repo>` with no `:<template>` loads that one registry and opens a picker scoped to its templates, returning the selected entry's fully qualified selector. This is the same picker as `pm generate` with no argument, narrowed to a single source.

### Generate a bare template name

```bash
pm generate :premise-rust-lib
```

The leading-colon shorthand names a template without a source. Premise resolves it against every official registry and generates the single match, so `:premise-rust-lib` resolves to the Cargo registry even though it is not the first official source. If the name matches no official registry, or matches more than one, Premise reports the ambiguity and asks you to qualify the source. A bare name is never searched across unofficial registries; reach those with a fully qualified `<owner>/<repo>:<template>` selector.

### Generate from a remote repository

```bash
pm generate cloudvoyant/premise-template:app
```

The remote repository must contain `premise.yaml` and `templates/<template>` for the selected manifest entry. Premise clones it through go-git and caches it below `~/.premise/templates`; later runs refresh that cache. Selectors accept `owner/repository:<template>`, HTTPS Git URLs such as `https://github.com/cloudvoyant/premise-template.git:app`, and SSH Git URLs followed by `:<template>`.

### Manifest

```yaml
workspace:
  name: example
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

`version` is the selected template declaration's version at generation time. If project registration or manifest saving fails after copying, Premise removes the newly created destination so output and provenance do not diverge.

### Task contracts

Applications and libraries must provide these tasks:

```text
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

### Current limitations

- Template source paths are fixed by template name, and generated roots are fixed by app/lib kind.
- Substitution changes UTF-8 file contents but not file or directory names.
- Remote templates use the repository's default branch.
- Private-repository authentication, concurrent cache locking, and offline mode are not available yet. Credential delegation for private registry sources is deferred to DIFF-150; go-git performs clones today.
- Premise refuses to merge into or replace an existing destination.
