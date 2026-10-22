# End-to-end tests

The suite uses Go's nested subtests. The top level names the feature. Each `when ...` context arranges a scenario and runs the command. Its `it ...` children describe the expected results.

| Suite      | Contexts                                                                                                                           |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `Scaffold` | Creating a lib, creating an app without infra, creating an app with infra, adding infra later, existing template validation        |
| `Infra`    | Setting up, planning, applying across environments, failed updates, querying, destroying                                           |
| `Secrets`  | Creating env files for each environment, failed pulls, running Mise tasks with app secrets                                         |
| `CI`       | Feature commits, merge to main, manual deployment to dev/stage/prod, failed checks, failed artifacts, preview rollback and cleanup |

Scenario actions and assertions stay in the suite files. Helpers build the CLI, copy fixtures, configure remote state, and execute processes. Each context can run on its own. Teardown removes test resources even if a scenario fails. Preview destruction and repeated cleanup also have explicit outcome tests.

## Local tests

```sh
PREMISE_TEST_FNOX=1 mise exec -- go test ./e2e -count=1 -v
```

This runs actual CLI commands and real Mise tasks. The scaffold cases generate Go apps and libraries that build, test, and run without third-party packages. The secrets cases use real fnox with test JSON documents. Existing template-validation regressions retain their tool shims.

CI cases calculate versions, run checks, and stage RC or stable files through `release:build`. They do not upload a GitHub release. Hosted workflow routing and publication remain separate from local artifact staging.

Select a scenario by its path:

```sh
mise exec -- go test ./e2e -run '^TestScaffold$/when_creating_a_lib' -v
PREMISE_TEST_FNOX=1 mise exec -- go test ./e2e -run '^TestSecrets$/when_running_Mise_tasks' -v
```

## Live infrastructure tests

Set the credentials, dedicated project, and existing state backend described in the [GCS fixture](testdata/pulumi/README.md), then run:

```sh
mise run e2e:infra
```

Live tests use real Pulumi and GCS. Each run selects unique backend prefixes, environment stacks, and bucket names. Dev, stage, prod, and preview use the same dedicated GCP project. The fixture uses two named state backends to test selection. Cleanup deletes the resource buckets and preserves the existing state bucket. Preview stacks use `preview-<branch-slug>-<10-character-hash>`. E2E branch names use `feature/static-<run-id>`. Pulumi assigns bucket names and exports them for `pm infra query`.

The live suite tests official Pulumi starter generation, tool setup, plans without changes, resource updates and failures, saved-state queries, and destruction. CI contexts test feature previews, migrations, native application rollback, merge cleanup, dev updates, and promotion of the same staged static page to dev/stage/prod without rebuilding it.

`PREMISE_BIN` selects a published CLI. Without it, the suite builds the checkout. Live tests require `PREMISE_TEST_GCS=1`; a skipped test does not prove live behavior. They run separately on demand and are not part of CI.
