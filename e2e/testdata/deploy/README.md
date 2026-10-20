# Application deployment and recovery fixture

This client owns a Go Pulumi program, two migrating applications, and one application without migrations.
The published PostgreSQL provider creates real databases in a temporary PostgreSQL cluster.
The published Command provider deploys retained release files to a running local HTTP application.
Application tasks query credentials through `pm infra query` and run real SQL migrations.

```text
client/
├── premise.yaml                 Prerequisites and application contracts
├── mise.toml                    Pinned tools and dependency installation
├── infra/                       Client-owned Pulumi program
├── tasks/main.go                HTTP app and application task implementation
└── apps/
    ├── api/mise.toml            migrate, health, rollback, cleanup
    ├── worker/mise.toml         migrate, health, rollback, cleanup
    └── website/mise.toml        health and rollback
```

Run from the Premise checkout with PostgreSQL tools on PATH:

```sh
PREMISE_TEST_POSTGRES=1 go test ./e2e \
  -run 'Test(Deployment|MigrationFailure|SharedMigrationGroup)' -count=1 -timeout 20m -v
```

The harness creates its own loopback-only PostgreSQL cluster and stops it during cleanup.
It uses an explicitly enabled local state backend. It creates no cloud resources.
The tests cover no migration tasks, parallel migration processes, ordered shared-history tasks,
failed migration gating, fresh-checkout phase handoff, and rejection of overlapping mutations.

The rollback scenario deploys A, migrates compatible B, deploys B, and fails B's health task.
Premise then invokes the client's rollback tasks with remembered artifact references.
Assertions query the HTTP application, PostgreSQL schema/data, and last-success record.
A remains the successful release. The B schema remains applied.

These local checks do not replace published-RC, hosted job-graph, or live cloud validation.
