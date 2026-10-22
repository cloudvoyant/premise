# Creating a project with infrastructure

## Add infrastructure

A Pulumi program declares your resources. A stack holds one environment’s state. Use one program with separate `dev`, `stage`, `prod`, and feature-branch preview stacks.

Create a client-owned program during workspace initialization:

```sh
pm init --infra --pulumi-lang go
# Or choose TypeScript and an explicit Pulumi pin.
pm init --infra --pulumi-lang typescript --pulumi-version 3.268.0
mise run infra:install
```

If `infra/Pulumi.yaml` already exists, use `pm init --infra` to adopt it. If you already initialized Premise without infrastructure, add it later:

```sh
pm infra setup --pulumi-lang go
# Adopt an existing program instead:
pm infra setup
```

Setup adds the missing `on-deploy.yml`, preserves existing files and tool pins, and leaves backend configuration to you. Without infrastructure, initialization omits that manual deployment workflow. `infra/Pulumi.yaml` selects the language runtime. Root `mise.toml` selects Pulumi and runtime versions. Use ordinary language imports to share resource declarations across environment stacks.

Declare `workspace.infra.directory`, backend choices, and environment values in `premise.yaml`. Supply an existing remote backend through a named selection or `PULUMI_BACKEND_URL`:

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

When premise.yaml declares infrastructure and provider credentials are configured, `on-commit.yml` deploys feature-branch previews. `on-merge.yml` deploys dev on main and cleans up previews after a same-repository PR merge or branch deletion. `on-deploy.yml` runs only when started manually. Persistent environments require interactive confirmation for destruction. Publishing a library on merge does not create a GitHub deployment or select a `production` environment. Publication tokens belong in repository or organization secrets.
