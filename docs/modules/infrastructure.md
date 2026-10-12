# Infrastructure Module

## Purpose

The infrastructure module invokes Pulumi for an explicit environment and connects project-owned tasks to the deployment lifecycle. Pulumi owns resource dependencies, provider configuration, state, and secret outputs. A program declares resources; a stack is an instance of that program; a component groups resources in the program.

## Design

### Environment configuration

Use one program for each environment's infrastructure. Its `dev`, `stage`, `prod` and feature-branch preview stacks have separate state. The program contains database and application resources. A shared program can own resources outside these environments.

```yaml
workspace:
  infra:
    bindings:
      web: {program: environment, artifact: .}
  projects:
    - name: environment
      path: infra/environment
      infra: {role: program, scope: environment}
```

Supply `PULUMI_BACKEND_URL` through the shell, CI, or a Mise/fnox environment. It selects an existing remote backend. No environment map or cloud project declaration is required in `premise.yaml`. Provider SDKs consume their usual Pulumi configuration and environment variables. For example, GCP project choices belong in Pulumi or fnox configuration, not in a Premise-specific GCP interface.

For repositories that want named backend choices, `workspace.infra.backend`, `backends`, and optional `environments.ENV.backend` provide an explicit selection. `--backend NAME` selects one of those declarations. Optional `environments.ENV.config` copies non-secret convenience settings at initialization; use ordinary `Pulumi.STACK.yaml` configuration when Premise should not own them. An explicitly selected backend takes precedence over the ambient URL. This supports GCS, S3, or Azure Blob state without assuming which provider deploys the resources.

The backend must exist before initialization. Hosted deployments use a configured remote DIY backend. Local tests can use an absolute `file://` URL with `allow_local: true`. A backend selection changes the state destination. It does not migrate existing state.

```sh
pm infra init --env dev
pm infra plan --env dev
pm infra apply --env dev --yes
pm infra output --env dev
pm infra test
```

A client-owned component needs a project declaration only when Premise should run its task contracts or select its resource targets. It does not need a registry template identifier.

```yaml
workspace:
  projects:
    - name: client-resources
      path: infra/components/client
      infra: {role: component}
```

Import the component from the environment program through the language's normal module imports. The Neon/Vercel example demonstrates this composition.

The project argument is optional. Omitting it selects the complete program for the environment. Selecting a component uses its declared resource targets in the same stack. A component does not receive its own stack.

```sh
pm infra bind web environment --artifact .output
pm infra plan --env preview --branch feature/example
```

The binding connects the app's artifact path to the program. Initialization supplies `premise:bindings` and an `artifacts` map to the program. A template must consume its declared inputs.

### Deployment order

Declare the database prerequisite targets and migration task in `workspace.deployment`. Targets identify resource URNs, which are Pulumi's unique resource names. They can contain `{stack}` and `{project}` placeholders. Premise inspects the full preview graph before the targeted update.

```yaml
workspace:
  deployment:
    records: .premise-releases
    record_outputs: [deploymentId, appURL]
    prerequisites:
      - "urn:pulumi:{stack}::{project}::*::db-*"
    migrations:
      - project: web
        task: migrate
        outputs: {DATABASE_URL: databaseURL}
    applications:
      - project: web
        health: health
        rollback: rollback
        outputs: {APP_URL: appURL}
    recovery:
      first_deploy: {project: web, task: cleanup}
```

Include all resources that the database needs in the prerequisite declaration. Premise includes required provider and parent resources. It rejects dependencies outside the selected set. Do not include application resources before the migration barrier.

```sh
pm deploy plan --env stage
pm deploy run --env stage --release COMMIT_SHA --artifact web=IMMUTABLE_ARTIFACT_ID
```

The executor initializes the stack, applies database prerequisites and runs migration tasks. It then previews and applies the full stack, including databases. It runs remaining task-managed deployments and application health tasks. An app without a `deploy` hook is deployed by Pulumi. `managed_by` remains accepted for compatibility but is optional; the presence of `deploy` selects the extra task.

Omit `migrations` when the project has no migration task. An app declares a `deploy` task only when Pulumi does not deploy it:

```yaml
applications:
  - project: web
    deploy: deploy
    health: health
    rollback: rollback
    outputs: {APP_URL: appURL}
```

Keep this declaration inside `workspace.deployment`, with the recovery and record settings above. The infrastructure program must not deploy the same app. Use `--backend NAME` to select another declared backend for an operation.

Migration tasks receive selected secret stack outputs in their child environment. Tasks also receive `PM_ENV`, `PM_BRANCH`, `PM_STACK`, `PM_RELEASE` and `PM_ARTIFACTS`. Application secrets come from the task project's fnox profile. The infra program has its own profile and receives ordinary environment variables; templates explicitly wrap secret resource inputs with `pulumi.ToSecret`. Artifact bindings do not transport application secrets. The migration tool owns schema history and locking.

### Recovery

The client must keep migrations compatible with the running application. Premise does not infer a down migration. A database recovery task can restore a backup or perform client-defined compensation.

After a rollout or health failure, Premise runs the declared rollback tasks. They receive the previous successful release and artifact identities. `PM_PREVIOUS_OUTPUTS` supplies declared non-secret deployment identities, such as the previous Vercel deployment ID. A task that changes Pulumi resources must preview its targeted recovery first. It must not reapply old database definitions or rerun old migrations.

A first deployment has no previous successful release. Declare a `first_deploy` recovery task. Optional `provision`, `migration` and `database` recovery tasks handle their respective failures. Recovery hooks do not automatically fetch app secrets: provisioning may have failed before those secrets existed. A hook that needs an app value resolves it explicitly through fnox. A failed recovery reports both errors and keeps recovery inputs available.

The record directory contains `last-success.json` and `last-run.json` under `PROGRAM/STACK`. This keeps different programs separate when they share a backend. It contains no secret output values. Store the directory durably across CI runners. The composite action restores and saves records in a GCS prefix. `records-url` can select a prefix separate from the state backend. The action appends the program and stack names to that prefix.

The deployment holds a local lock through both updates, migrations and recovery. CI concurrency must serialize that whole sequence with cancellation disabled. Pulumi's backend lock covers each update separately. It does not lock the migration task.

### Template components and client composition

Registry templates contain Go project components built with published Pulumi providers. A Neon database template and a Vercel application template are separate components. Neither is a new provider. Premise renders their names and declared questionnaire substitutions when it scaffolds each project.

```text
registry/
  premise.yaml                         # template declarations and questions
  templates/neon-db/
    component.go                       # Go ComponentResource using Neon SDK
    go.mod, go.sum, mise.toml
  templates/vercel-app/
    component.go                       # Go ComponentResource using Vercel SDK
    go.mod, go.sum, mise.toml

client/
  premise.yaml                         # projects, bindings, optional task hooks
  apps/web/                            # project Mise tasks and app source
  apps/admin/
  infra/environment/
    Pulumi.yaml                        # one Go program, not one program per app
    main.go                            # imports and composes the components
    go.mod, go.sum, mise.toml
    fnox.toml                          # program's selected secret references
    Pulumi.dev.yaml                    # normal Pulumi configuration, when used
  infra/components/database/           # generated Neon component
  infra/components/web-deployment/     # generated Vercel component instance
  infra/components/admin-deployment/   # another instance of the same template
  infra/components/client/             # component authored by the client
```

Generate the Vercel template twice with distinct names. The two rendered Go modules have distinct module paths. The environment program imports each module with its own alias and calls each constructor with distinct Pulumi resource names. Alternatively, import one rendered module and call its constructor twice. Resource parents and constructor names keep both instances separate in the same stack. Binding `web` and `admin` supplies each app's relative source path; the program connects the database output to both constructors through normal Pulumi outputs.

```sh
pm g REGISTRY:neon-db --answer name=database
pm g REGISTRY:vercel-app --answer name=web-deployment
pm g REGISTRY:vercel-app --answer name=admin-deployment
pm infra bind web environment --artifact .
pm infra bind admin environment --artifact .
pm infra init --env preview
pm infra plan --env preview
pm infra apply --env preview --yes
```

The client owns `main.go` and its Go module imports. Component templates can provide the initial composition in a client template; Premise does not discover resource types or generate provider-specific wiring during apply. `pm infra test` runs the declared component/program test tasks. The embedded E2E client fixture demonstrates this structure against the tandem templates.

### Public API

| API                                            | Responsibility                                             |
| ---------------------------------------------- | ---------------------------------------------------------- |
| `InfraStack(options)`                          | Resolve a persistent or branch-specific stack identity.    |
| `ResolveInfrastructure(root, options)`         | Select the program, backend, config, and resource targets. |
| `RunInfra(ctx, root, operation, options, ...)` | Invoke Pulumi init, plan, apply, output, or destroy.       |
| `BindInfra(root, app, program, artifact)`      | Save an app path binding to one program.                   |
| `TestInfrastructure(ctx, root, project, ...)`  | Run project-owned infra tests.                             |
| `PlanDeployment` / `ResolveDeploymentPlan`     | Report task order and recovery-record destination.         |
| `RunDeployment(ctx, root, options, ...)`       | Apply infra with optional task hooks and failure recovery. |

Infra commands take an optional project. Omitting it selects the complete stack. A component filter uses its declared resource targets inside that stack. `pm deploy run` updates the complete environment, so it does not take a component filter.

## Implementation Details

### Environment identities

| Environment | Pulumi stack               | Lifetime                                       |
| ----------- | -------------------------- | ---------------------------------------------- |
| `dev`       | `dev`                      | Persistent development infrastructure.         |
| `stage`     | `stage`                    | Persistent release validation infrastructure.  |
| `prod`      | `prod`                     | Persistent production infrastructure.          |
| `preview`   | `preview-BRANCH_SLUG-HASH` | One isolated stack per feature branch.         |
| `--shared`  | `shared`                   | Optional resources outside environment stacks. |

`InfraStack` lowercases the branch, replaces unsupported characters with hyphens, limits the slug to 40 characters, and appends the first five SHA-256 bytes of the original branch. Thus `feature/a` and `feature-a` have separate stacks. Repeated pushes use the same identity; deletion supplies the original branch to destroy it. The CLI infers the current branch when `--env preview` omits `--branch`.

Initialization selects or creates that stack and supplies `premise:environment`, `premise:branch`, `premise:stack`, `premise:bindings`, and the program's `artifacts` map. These are runtime parameters for the same program. Provider accounts, project IDs, regions, and authentication remain provider configuration.

The same program creates previews from feature-branch pushes. The configuration supplies branch identity and environment parameters. A new PR or branch-specific program is not required.

The generated workflow pins action and binary version `v0.5.0-rc.227` because it supplies the deployment interface introduced here. Move both pins together after a compatible stable release.

Set `PREMISE_DEPLOY_ENABLED=true` to enable the generated feature-branch deployment workflow. Configure GCP workload identity, backend selection and CI administration secrets. The preview stack name includes a stable branch hash. Further pushes update the same stack.

Branch deletion invokes cleanup with the deleted branch identity. It removes only that preview stack. Shared resources, project containers and the state backend remain. Cleanup uses the default branch's program, so retain compatible resource definitions there.

### Interrupted deployments

A cancelled process can leave partial resource changes or an interrupted lock. It does not guarantee that recovery tasks ran. Inspect the stack, migration history and last-run record before retrying.

If a stored lock remains, identify its interrupted run before removing it. Do not overwrite a successful release record to clear a failure. Restore the previous app with its pinned inputs and current database state. Record any manual repairs and remaining resources.

### Secret environment

The program can declare `secrets` using the same fnox profile contract as app projects. Premise resolves only that program's declared keys and passes their names unchanged to Pulumi. For two apps, use distinct keys such as `WEB_SECRET` and `ADMIN_SECRET` and let the client program assign them to the corresponding component. No `PM_APP_SECRETS` transport or automatic app-secret aggregation is used. See [Secret management](../secret-management.md) for fnox, Mise, CI credentials, and local env files.

## References

- [Secret management](../secret-management.md)
- [Pulumi targeted updates](https://www.pulumi.com/docs/iac/operations/stack-management/targeted-updates/)
- [Pulumi state and backends](https://www.pulumi.com/docs/iac/concepts/state-and-backends/)
