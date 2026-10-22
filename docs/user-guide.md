# User Guide

Premise creates projects from templates and runs their tasks through Mise. A workspace groups applications, libraries, and infrastructure in one repository.

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

## Check a change

```sh
pm ci flow on-commit --release none
pm ci plan --flow on-commit --json
pm projects ls --flow on-commit --json
```

The flow runs project checks. The plan reports the required runners without running tasks. CI uses the same commands and can create separate project/platform jobs. See [CI](modules/ci.md).

## Add infrastructure

A Pulumi program declares your resources. A stack holds one environment’s state. Use one program with separate `dev`, `stage`, `prod`, and feature-branch preview stacks.

Create a client-owned program during workspace initialization:

```sh
pm init --infra --infra-language go
# Or choose TypeScript and an explicit Pulumi pin.
pm init --infra --infra-language typescript --pulumi-version 3.268.0
mise run infra:install
```

If `infra/Pulumi.yaml` already exists, use `pm init --infra` to adopt it.
If you already initialized Premise without infrastructure, add it later:

```sh
pm infra setup --language go
# Adopt an existing program instead:
pm infra setup
```

Setup adds the missing `on-deploy.yml`, preserves existing files and tool pins, and leaves backend configuration to you.
Without infrastructure, initialization omits that manual deployment workflow.
The file selects the language runtime. Root `mise.toml` selects Pulumi and runtime versions.
Use ordinary language imports to share resource declarations across environment stacks.

Declare `workspace.infra.directory`, backend choices, and environment values in `premise.yaml`.
Supply an existing remote backend through a named selection or `PULUMI_BACKEND_URL`:

```sh
export PULUMI_BACKEND_URL=s3://YOUR_STATE_BUCKET/product
pm infra auth
pm infra init --env dev
pm infra plan --env dev
pm infra apply --env dev --yes
pm infra query --env dev --resource uploads --property arn
```

The commands select the whole environment stack and accept no application project argument.
See the [infrastructure module](modules/infrastructure.md) for Go and TypeScript layouts and full configuration.

## Create a branch preview

On a feature branch, run:

```sh
git switch -c feature/login
pm infra init --env preview
pm infra plan --env preview
pm infra apply --env preview --yes
```

Premise uses the current branch to select a stable preview stack. Further pushes update the same stack. The same program handles each branch through parameters.

Destroy this preview when you finish:

```sh
pm infra destroy --env preview
```

After you enable `PREMISE_DEPLOY_ENABLED` and configure provider credentials, `on-commit.yml` deploys feature-branch previews. `on-merge.yml` cleans them up after a same-repository PR merge or branch deletion. `on-deploy.yml` runs only when started manually. Persistent environments require interactive confirmation for destruction.
Publishing a library on merge does not create a GitHub deployment or select a `production` environment. Publication tokens belong in repository or organization secrets.

## Deploy with migrations

Use `pm ci flow on-deploy` for the complete environment sequence:

```sh
export PM_RELEASE=COMMIT_SHA
export PM_ARTIFACTS='{"web":"IMMUTABLE_ARTIFACT_REFERENCE"}'
pm ci flow on-deploy --environment stage --release none
```

For an existing stack, the order is migrate, infra plan, infra apply, then application deploy and health checks.
For a new stack, migration runs after the first apply. Applications without a migration task skip that step.
Clients must apply database infrastructure changes separately before schema migrations and keep migrations backward compatible.

You can also run `pm migrate`, `pm infra plan`, `pm infra apply`, and `pm deploy` individually with `--env ENV`.
`pm deploy` only deploys and checks application artifacts; it has no subcommands.
The complete CI flow retains successful artifact references and attempts recovery on failure.
See the [infrastructure guide](modules/infrastructure.md) for configuration and recovery inputs.

## Pull application secrets

Declare the application's fnox configuration and environment profiles in `premise.yaml`. Fnox retrieves JSON documents as temporary files locally and in CI.

```sh
pm secrets exec --env dev --project web -- mise run start
pm secrets pull --env dev --project web
```

`exec` supplies JSON file paths to the child process. `pull` runs the project's `secrets:pull` Mise task and writes its output to a private ignored env file. The task can combine secret fields with `pm infra query` values.

Deployment and migration tasks automatically use fnox when the application declares secrets. CI authenticates directly to the secret provider. Deployment OAuth credentials and protected CI tokens stay separate from application documents. See [Secret management](secret-management.md) and the [architecture diagrams](architecture/secrets.md).

## Publish a release

Check the next version or build files without publication:

```sh
pm version current
pm version next
pm release --dry-run
pm release --build
```

Publish from a clean checkout after checks pass:

```sh
pm release
```

The generated main-branch workflow publishes stable releases. It creates or reuses the release tag, publishes downloadable files, then publishes eligible language packages.

For an RC that another repository must test, include the exact marker in a feature-branch commit:

```sh
git commit -m 'feat: add deployment support [publish-rc]'
git push
```

Wait for publication before selecting that RC in the dependent repository. Keep the action and CLI on the same version. Pull requests run checks without publishing. See [Release](modules/release.md) for staged publication and credential setup.

## Create templates

Create a registry and test its templates:

```sh
pm init --kind template-registry
pm template init app
pm template ls
pm template test
```

Register template questions and literal substitutions in `premise.yaml`. Templates remain valid projects before generation. Root files listed in `template_registry.workspace_files` are shared with the client. Premise asks about conflicting root files before it writes the generated project.

Template registries provide application and library templates. Clients own infrastructure code under `infra/` and import published providers directly. See [Generation](generation.md) and [Infrastructure](modules/infrastructure.md).

## Limits

Generation creates new projects. It does not update existing ones. Remote selectors use the default branch and need network access. Private-template authentication and offline operation remain separate work.

Pulumi state uses an empty passphrase for now. Backend access controls and storage encryption protect it. Custom passphrases are outside the current interface. See [state protection](providers/pulumi.md) before adopting an encrypted stack.
