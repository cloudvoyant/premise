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

## Native package-manager generation

`TestScaffold` uses real CLI generation and Mise tasks for dependency-free Go, Cargo, and Bun apps with local sibling libraries. The fixtures pin Go 1.25.14, Rust 1.88.0, and Bun 1.4.2. Mise installs those tools from the generated configurations. These scenarios need no registry credentials, cloud resources, or external publication. Native checks are not silently skipped.

```sh
mise exec -- go test ./e2e -run '^TestScaffold$/when_generating_native_workspaces/when_generating_a_go_workspace' -count=1 -v
mise exec -- go test ./e2e -run '^TestScaffold$/when_generating_native_workspaces/when_generating_a_cargo_workspace' -count=1 -v
mise exec -- go test ./e2e -run '^TestScaffold$/when_generating_native_workspaces/when_generating_a_bun_workspace' -count=1 -v
mise exec -- go test ./e2e -run '^TestScaffold$/when_combining_native_workspaces/when_adding_bun_then_cargo_to_Go' -count=1 -v
mise exec -- go test ./e2e -run '^TestScaffold$/when_combining_native_workspaces/when_adding_cargo_then_bun_to_Go' -count=1 -v
mise exec -- go test ./e2e -run '^TestScaffold$/when_extending_an_existing_standalone_Cargo_workspace' -count=1 -v
mise exec -- go test ./e2e -run '^TestScaffold$/when_native_generation_inputs_conflict' -count=1 -v
```

The mixed scenarios retain existing Go source, add both registry orders, and generate a language-neutral project. They verify root membership, sibling resolution, retained client identity, and published native lockfiles before live dependency installation. Root `pm install` does not require a root task when only project install contracts exist.

The existing registry-contribution scenario uses public `core.Generate` with real native tools because it supplies deterministic conflict decisions. The CLI matrix covers ordinary generation separately. Its old success-only shim and exact process-count assertions are removed; controlled failure doubles remain for rollback tests.

## Live infrastructure tests

Set the credentials, dedicated project, and existing state backend described in the [GCS fixture](testdata/pulumi/README.md), then run:

```sh
mise run e2e:infra
```

Live tests use real Pulumi and GCS. Each run selects unique backend prefixes, environment stacks, and bucket names. Dev, stage, prod, and preview use the same dedicated GCP project. The fixture uses two named state backends to test selection. Cleanup deletes the resource buckets and preserves the existing state bucket. Preview stacks use `preview-<branch-slug>-<10-character-hash>`. E2E branch names use `feature/static-<run-id>`. Bucket names start with `storage-dev`, `storage-stage`, `storage-prod`, or `storage-static-<run-id>`. Pulumi adds a random suffix for global uniqueness and exports the final name for `pm infra query`. The preview slug omits the `feature/` prefix.

The live suite tests official Pulumi starter generation, tool setup, plans without changes, resource updates and failures, saved-state queries, and destruction. CI contexts test feature previews, migrations, native application rollback, merge cleanup, dev updates, and promotion of the same staged static page to dev/stage/prod without rebuilding it.

`PREMISE_BIN` selects a published CLI. Without it, the suite builds the checkout. Live tests require `PREMISE_TEST_GCS=1`; a skipped test does not prove live behavior. They run separately on demand and are not part of CI.
