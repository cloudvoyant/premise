# Creating and working with projects

Premise creates applications and libraries from templates and runs their Mise tasks.

## Install

Install Premise and its `pm` alias:

```sh
curl -fsSL https://raw.githubusercontent.com/cloudvoyant/premise/main/install.sh | bash
```

Install [Mise](https://mise.jdx.dev/) for project tools. Update Premise with `pm update` or select a release with `pm update VERSION`.

## Create a workspace

```sh
mkdir my-product
cd my-product
pm init
pm g :premise-tanstack-start-app
pm install
```

Answer the template questions to name your app. Premise records the project in `premise.yaml` and places its files under `apps/`. Library templates use `libs/`.

Choose a registry or a specific template:

```sh
pm g cloudvoyant/premise-bun
pm g cloudvoyant/premise-bun:premise-hono-api
pm g ../my-registry:app
```

`pm g` opens the official template picker. A local registry path lets you test unmerged template changes. Remote selectors use the repository’s default branch.

## Run project tasks

For an app named `web`, run:

```sh
pm run web:dev
pm run web:test
pm run build
```

The last command runs the root `build` task. Tasks live in each project's `mise.toml`. Arguments after the task name pass through to Mise.

Run a task across the workspace:

```sh
mise run --jobs 1 '//...:build'
mise run --jobs 1 '//...:test'
```

## Create templates

Create a registry and test its templates:

```sh
pm init --kind template-registry
pm template init app
pm template ls
pm template test
```

Register template questions and literal substitutions in `premise.yaml`. Templates remain valid projects before generation. Root files listed in `template_registry.workspace_files` are shared with the client. Premise asks about conflicting root files before it writes the generated project.

Template registries provide application and library templates. Clients own infrastructure code under `infra/` and import published providers directly. See [Generation](../generation.md) and [Infrastructure](../modules/infrastructure.md).

## Limits

Generation creates new projects. It does not update existing ones. Remote selectors use the default branch and need network access. Private-template authentication and offline operation remain separate work.

Pulumi state uses an empty passphrase for now. Backend access controls and storage encryption protect it. Custom passphrases are outside the current interface. See [state protection](../providers/pulumi.md) before adopting an encrypted stack.
