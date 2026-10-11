---
base_commit: 'ce02da5df5eb1cf55743ab2996f74956db03320f'
---

# PoC: Pulumi infrastructure and application deployment

This experimental branch adds Pulumi operations to Premise. All fixtures and tests live in this repository. No premise-bun or premise-pulumi checkout is required. The former premise-pulumi PoC branch has been removed.

## Scope and layout

Generate infrastructure programs, bind application artifacts once, and plan/apply the whole workspace for an explicit environment. The Go fixture creates one private GCS bucket per environment and one separate shared bucket. Backend provisioning and team state management belong to a different PoC.

```text
client/
  apps/site/
  infra/
    environment/                 # Reserved environment program
      Pulumi.yaml
      premise-infra.yaml
    projects/shared/             # Shared program and shared stack
    components/<name>/           # Optional client-owned components
  .pulumi-state/                 # Ignored file backend
  premise.yaml
```

`pm g` puts the reserved infra name `environment` at `infra/environment`. Other programs go to `infra/projects/<name>`. Each program is one component or provider-backed unit and may depend on published packages, providers, or client-owned components. Premise does not copy or publish nested component libraries.

Shared scope is infrastructure-specific configuration:

```yaml
workspace:
  projects:
    - name: shared
      template: /path/to/registry:gcs-bucket
      path: infra/projects/shared
      infra:
        shared: true
```

Mise discovers build, test, and lint tasks for programs and components. The infra parent directory is not a Mise project. Pulumi stack operations run directly through the Pulumi CLI. Language runtimes and dependency packages belong to each program.

## Commands and environments

Environments are `dev`, `stage`, `prod`, and `preview`. A preview requires `--branch <branch>` and gets a deterministic readable stack name with a hash suffix. Generated programs named `shared` default to shared scope; `--shared` selects shared programs alone.

```sh
pm infra init --env dev --gcp-project YOUR_GCP_PROJECT --location US
pm infra plan --env dev
pm infra apply --env dev --yes
pm infra output environment --env dev
pm infra plan --env preview --branch feature/example
pm infra destroy --env preview --branch feature/example
pm infra test
```

Plan/apply select the whole workspace by default and run shared programs first. A project argument scopes the operation. `preview` is an alias for `plan`. Projects run sequentially; a failure stops later projects, but successful earlier updates remain. No command silently selects prod or applies every environment.

Preview destroy approves deletion and removes the preview stack, excluding shared infrastructure. Dev/stage/prod/shared destroy requires Pulumi's manual confirmation and rejects `--yes`. Stable destroy overrides Pulumi's ambient skip-confirmation setting for that subprocess.

`pm infra test` runs registered program/component test tasks without selecting a stack. Client-owned components under `infra/components/<name>` can be registered with a name and path, without a template selector. They join CI checks but are not deployment targets. Infra projects are excluded from application package publication.

## Bind once per project

```sh
pm infra bind site environment --artifact dist
pm infra bind site shared --shared --artifact dist
```

The template declares its configuration key in `premise-infra.yaml`:

```yaml
artifact_config_key: artifacts
```

Binding writes project defaults in `Pulumi.yaml`:

```yaml
config:
  artifacts:
    value:
      site: ../../apps/site/dist
```

Defaults apply to current and future environment stacks; existing stack overrides take precedence. Binding can happen before stack initialization or building. Repeating the same binding is allowed; conflicting bindings are rejected. Stack configuration and encrypted secrets remain untouched. Project YAML formatting/comments may change.

`--shared` records shared scope. Changing an environment project with non-shared stack configuration into shared scope is rejected; stack migration must be explicit. This local guard cannot detect state-only stacks that have no local configuration.

The fixture uploads regular files as GCS objects with content checksums and stable identities. Pulumi manages uploads, replacements, and removal of previously managed objects together with the bucket. A bound artifact directory must exist and contain files when the program runs. The bucket is private and force deletion is disabled. This is not public website hosting, a CDN, or an atomic release mechanism.

## Self-contained verification

Use Go 1.25, Mise, and Pulumi 3.200.0 for real local-state checks.

```sh
mise run build
mise run test
mise run lint
mise run format:check
mise run run:infra
mise run run:buckets
mise exec pulumi@3.200.0 -- mise run run:local-state
```

`TestPulumiWorkflow` uses committed registries and process doubles to check CLI generation, binding, task dispatch, failure ordering, environment selection, and destroy arguments. The static app fixture is a small HTML artifact, not a Svelte or Bun integration test.

`run:buckets` runs the actual Go GCS program's resource-mock tests. `run:local-state` uses real Mise and Pulumi, builds the Go fixture, checks configuration inheritance for dev/stage/prod, removes an empty preview stack, and verifies stable destroy refuses non-interactive execution. These tests create no cloud resources. Live GCP plan/apply has not been verified.

A disposable client can use the same local fixtures:

```sh
POC_ROOT="$HOME/.herdr/worktrees/premise/poc-pulumi"
export PATH="$POC_ROOT/bin:$PATH"
mkdir client-pulumi-poc
cd client-pulumi-poc
pm init
pm g "$POC_ROOT/e2e/testdata/pulumi/infra-registry:gcs-bucket" --answer name=shared
pm g "$POC_ROOT/e2e/testdata/pulumi/infra-registry:gcs-bucket" --answer name=environment
pm g "$POC_ROOT/e2e/testdata/pulumi/app-registry:static-app" --answer name=site
pm infra bind site environment
pm run site:build
```

## Provider ownership: GCP, Neon, and Vercel

`workspace.providers.infra: pulumi` selects the infrastructure engine, not a cloud provider. A single Pulumi program can use GCP, Neon, and Vercel together. The program declares and pins SDK dependencies in its language manifest, such as `go.mod` or `package.json`; Pulumi supplies the corresponding resource plugins. Premise does not maintain another provider registry. See [Pulumi package dependencies](https://www.pulumi.com/docs/iac/guides/basics/language-essentials/packages-and-dependencies/) and [resource providers](https://www.pulumi.com/docs/iac/concepts/providers/).

Provider configuration is stack-specific, while shared defaults can live in Pulumi.yaml. Credentials should come from the CI secret store or encrypted Pulumi config. They must not be committed as plain text in template YAML. A program can declare explicit provider instances for multiple accounts/projects and pass them to resources.

For provider credentials, the Neon provider supports `NEON_API_KEY` and the Vercel provider supports `VERCEL_API_TOKEN`; GCP uses the configured Google credentials. Those are existing provider inputs, not Premise-specific variables. See [Neon provider configuration](https://www.pulumi.com/registry/packages/neon/api-docs/provider/) and [Vercel provider configuration](https://www.pulumi.com/registry/packages/vercel/api-docs/provider/).

The implemented fixture uses only GCP, with SDK versions pinned in its go.mod/go.sum. Neon and Vercel are design requirements, not implemented or validated fixtures. The current `pm infra init` requires GCP project and bucket location flags and writes GCP-specific configuration. This is a known PoC restriction, not a general multi-provider interface. A feature version should separate stack creation from template/provider-specific configuration and support validated namespaced inputs without requiring GCP flags. Provider credentials and account selection must remain explicit per environment.

## Stateful infrastructure and migrations

Pulumi provisions the database service, instance, branch, networking, and access policy. The application owns versioned schema/data migrations. They should be separate project-defined tasks, not incidental side effects of an infrastructure preview or resource update.

The existing task escape hatch can dispatch an app-owned task with environment and resolved stack inputs:

```sh
pm deploy run site --env stage --task migrate
pm deploy run site --env stage --task deploy
```

Tasks receive `--environment stage --stack stage`; preview tasks also receive `--branch`. They must declare or parse those inputs. Task names are project-defined: Premise does not currently enforce a migration contract, resolve a connection string, or sequence these commands with infrastructure apply.

A future deployment flow should provision/apply infra, obtain that environment's connection details without exposing secrets, run migrations, and deploy the selected artifact. A failed migration must stop application rollout. Backward-compatible additive migrations can precede a rollout; destructive schema changes should be delayed until old application versions no longer depend on them. Database migration rollback is a separate decision from reverting application artifacts or Pulumi state.

Stable databases should survive routine application releases and generally use explicit resource protection, backups, and approved destruction procedures. Preview databases may use isolated per-branch databases or Neon branches; the provider and application own that mapping. Preview teardown must target the isolated resource and must never remove a shared production database. This PoC does not yet verify database provisioning, migrations, database protection, or migration sequencing.

## Lifecycle and remaining limits

On commit, build/test/lint projects. On merge, build and publish artifacts, then preview explicitly selected environments. On deploy, restore the intended artifact and apply its bound infrastructure, or dispatch a project-defined deployment task. Promotion should reuse the same artifact across environments. Hosted workflows, automatic environment orchestration, artifact restoration, migration sequencing, approvals, and shared-state concurrency are not implemented here.

State defaults to `file://<workspace>/.pulumi-state`. An absolute file backend override is supported, including paths containing reserved URL characters. Remote backends, cross-runner locking, team secrets, and backend migration belong to a separate PoC.

## Known external compatibility issue

The attempted Bun task-directory simplification used `dir = "{{ vars.monorepo_root }}"`, with the variable defined in the workspace root. Premise's source-template contract runner sets a Mise discovery ceiling at the template parent, which hides that root config. The isolated contract then fails with `Field monorepo_root is not defined`. On Mise 2026.9.4, the built-in `MISE_MONOREPO_ROOT` is a task-runtime value and is not available while rendering `dir`.

The premise-bun edits have been reverted. No external repository fix is part of this PoC. This compatibility issue should be addressed separately, with an explicit decision about the registry-root context available during template tests. The current self-contained fixtures do not depend on the proposed variable.
