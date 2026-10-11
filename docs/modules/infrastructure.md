# Infrastructure deployment

A Pulumi program declares resources. A stack is an instance of that program for an environment. An imported component groups resources within the program.

## Environment configuration

Use one program for each environment's infrastructure. Its `dev`, `stage`, `prod` and feature-branch preview stacks have separate state. The program contains database and application resources. A shared program can own resources outside these environments.

```yaml
workspace:
  infra:
    backend: remote
    backends:
      remote:
        url: gs://your-existing-state-bucket
    environments:
      dev:
        config: {gcp:project: your-gcp-project, location: US}
      stage:
        config: {gcp:project: your-gcp-project, location: US}
      prod:
        config: {gcp:project: your-gcp-project, location: US}
      preview:
        config: {gcp:project: your-gcp-project, location: US}
  projects:
    - name: environment
      template: cloudvoyant/premise-pulumi:gcs-bucket
      path: infra/environment
      infra: {role: program, scope: environment}
```

The backend must exist before initialization. Hosted deployments use a configured remote DIY backend. Local tests can use an absolute `file://` URL with `allow_local: true`. A backend selection changes the state destination. It does not migrate existing state.

```sh
pm infra init --env dev
pm infra plan --env dev
pm infra apply --env dev --yes
pm infra output --env dev
pm infra test
```

The project argument is optional. Omitting it selects the complete program for the environment. Selecting a component uses its declared resource targets in the same stack. A component does not receive its own stack.

```sh
pm infra bind web environment --artifact .output
pm infra plan --env preview --branch feature/example
```

The binding connects the app's artifact path to the program. Initialization supplies `premise:bindings` and an `artifacts` map to the program. A template must consume its declared inputs.

## Deployment order

Declare the database prerequisite targets and migration task in `workspace.deployment`. Targets identify resource URNs, which are Pulumi's unique resource names. They can contain `{stack}` and `{project}` placeholders. Premise inspects the full preview graph before the targeted update.

```yaml
workspace:
  deployment:
    records: .premise-releases
    record_outputs: [deploymentId, appURL]
    prerequisites:
      - "urn:pulumi:{stack}::{project}::neon:index/branch:Branch::db-branch"
    migrations:
      - project: web
        task: migrate
        outputs: {DATABASE_URL: databaseURL}
    applications:
      - project: web
        managed_by: infra
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

The executor initializes the stack, applies database prerequisites and runs migration tasks. It then previews and applies the full stack, including databases. It runs remaining task-managed deployments and application health tasks. An infrastructure-managed app does not receive a separate deployment task.

Migration tasks receive selected secret stack outputs in their child environment. Tasks also receive `PM_ENV`, `PM_BRANCH`, `PM_STACK`, `PM_RELEASE` and `PM_ARTIFACTS`. Application secrets can come from the project's fnox profile. The migration tool owns schema history and locking.

## Recovery

The client must keep migrations compatible with the running application. Premise does not infer a down migration. A database recovery task can restore a backup or perform client-defined compensation.

After a rollout or health failure, Premise runs the declared rollback tasks. They receive the previous successful release and artifact identities. `PM_PREVIOUS_OUTPUTS` supplies declared non-secret deployment identities, such as the previous Vercel deployment ID. A task that changes Pulumi resources must preview its targeted recovery first. It must not reapply old database definitions or rerun old migrations.

A first deployment has no previous successful release. Declare a `first_deploy` recovery task. Optional `provision`, `migration` and `database` recovery tasks handle their respective failures. A failed recovery reports both errors and keeps recovery inputs available.

The record directory contains `last-success.json` and `last-run.json` for each stack. It contains no secret output values. Store the directory durably across CI runners. The composite action restores and saves records in a GCS prefix. `records-url` can select a prefix separate from the state backend.

The deployment holds a local lock through both updates, migrations and recovery. CI concurrency must serialize that whole sequence with cancellation disabled. Pulumi's backend lock covers each update separately. It does not lock the migration task.

## Feature-branch previews

The same program creates previews from feature-branch pushes. The configuration supplies branch identity and environment parameters. A new PR or branch-specific program is not required.

The generated workflow pins action and binary version `v0.5.0-rc.203` because it supplies the deployment interface introduced here. Move both pins together after a compatible stable release.

Set `PREMISE_DEPLOY_ENABLED=true` to enable the generated feature-branch deployment workflow. Configure GCP workload identity, backend selection and CI administration secrets. The preview stack name includes a stable branch hash. Further pushes update the same stack.

Branch deletion invokes cleanup with the deleted branch identity. It removes only that preview stack. Shared resources, project containers and the state backend remain. Cleanup uses the default branch's program, so retain compatible resource definitions there.

## Interrupted deployments

A cancelled process can leave partial resource changes or an interrupted lock. It does not guarantee that recovery tasks ran. Inspect the stack, migration history and last-run record before retrying.

If a stored lock remains, identify its interrupted run before removing it. Do not overwrite a successful release record to clear a failure. Restore the previous app with its pinned inputs and current database state. Record any manual repairs and remaining resources.

## References

- [Secret management](../secret-management.md)
- [Pulumi targeted updates](https://www.pulumi.com/docs/iac/operations/stack-management/targeted-updates/)
- [Pulumi state and backends](https://www.pulumi.com/docs/iac/concepts/state-and-backends/)
