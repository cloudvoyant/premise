# Creating a project with infrastructure

## Add infrastructure

A Pulumi program declares your resources. A stack holds one environment’s state. Premise organizes infrastructure into `infra/env` for per-environment stacks (`dev`, `stage`, `prod`, and feature-branch previews) and `infra/shared` for project-wide or organization-wide assets shared across stacks. Custom repo-local Pulumi components can live in an optional `infra/components/` directory.

Create both client-owned programs during workspace initialization:

```sh
pm init --infra --pulumi-lang go
# Or choose TypeScript and an explicit Pulumi pin.
pm init --infra --pulumi-lang typescript --pulumi-version 3.268.0
mise run infra:install
```

If you already initialized Premise without infrastructure, add it later:

```sh
pm infra setup --pulumi-lang go
```

Setup creates `infra/env` and `infra/shared`, adds the missing `on-deploy.yml`, preserves existing workspace files and tool pins, and leaves backend configuration to you. If anything already exists under `infra/`, setup stops without modifying files. Without infrastructure, initialization omits the manual deployment workflow. Each program's `Pulumi.yaml` selects its language runtime, and root `mise.toml` pins Pulumi and runtime versions. Use ordinary language imports (for example from `infra/components/`) to share code across stacks.

Declare `workspace.infra.directory`, `workspace.infra.shared_directory`, backend choices, and environment values in `premise.yaml`. Platform repositories that manage only organization-wide shared resources can omit `infra/env` and declare only `shared_directory: infra/shared`. Supply an existing remote backend through a named selection or `PULUMI_BACKEND_URL`:

```sh
export PULUMI_BACKEND_URL=gs://YOUR_STATE_BUCKET/product
pm infra auth
pm infra init --env dev
pm infra plan --env dev
pm infra apply --env dev --yes
pm infra query --env dev --resource uploads --property arn
```

The commands select the whole environment stack and accept no application project argument. See the [infrastructure module](../modules/infrastructure.md) for Go and TypeScript layouts and full configuration.

## Create a branch preview

On a feature branch, run:

```sh
git switch -c feature/login
pm infra init --env preview
pm infra plan --env preview
pm infra apply --env preview --yes
```

Premise uses the current branch to select a stable preview stack. For `feature/login`, `premise:environmentName` is `login` and the stack is `preview-login-<hash>`. Use the environment name in resource names, such as `storage-login`, and let the provider add a suffix for globally unique bucket names. Further pushes update the same stack. The same program handles each branch through parameters.

Destroy this preview when you finish:

```sh
pm infra destroy --env preview
```

When `premise.yaml` declares infrastructure and provider credentials are configured, `on-commit.yml` deploys feature-branch previews when `infra/env/Pulumi.yaml` exists. `on-merge.yml` applies `infra/shared` and `dev` in parallel on `main` and cleans up previews after a same-repository PR merge or branch deletion. `on-deploy.yml` runs only when started manually and supports `shared`, `dev`, `stage`, `prod`, and `preview`. Persistent environments (including `shared`) require interactive confirmation for destruction. Publishing a library on merge does not create a GitHub deployment or select a `production` environment. Publication tokens belong in repository or organization secrets.
