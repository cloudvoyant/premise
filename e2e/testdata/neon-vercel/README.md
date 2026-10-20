# Client-owned Neon and Vercel example

This directory preserves the useful provider composition from the retired template approach.
The client owns the Go program and imports published providers. It has no Premise registry or infrastructure project registration.

```text
client/
├── mise.toml
└── infra/
    ├── Pulumi.yaml             Go runtime and published Neon package generation
    ├── go.mod
    ├── go.sum
    ├── main.go                 Connect the database and application
    ├── database.go             Branch, endpoint, role, database, secret URL
    └── application.go          Vercel deployment and environment alias
```

Copy the program into a client workspace with a TanStack Start application.
Use `pm init --infra` to adopt it. Run `mise run infra:install` to install dependencies and generate the parameterized Neon SDK.
SDK generation uses the published Terraform bridge and Neon provider. No provider implementation lives in this repository.

The program expects non-secret `client:neonProjectId`, `client:neonParentId`, `client:vercelProjectId`, `client:vercelTeamId`, and `client:aliasPrefix` configuration.
Use dedicated test projects. Use a short alias prefix, such as `pm`. The program derives a separate alias from each stack name.
The database branch name includes the selected stack.

Applications own migrations and query the exported `databaseUrl` explicitly with `--show-secrets`.
The program marks that URL secret and passes it only to the server application.
It also exports `deploymentId` and `applicationUrl` for health checks and retained release metadata.

The `web` artifact reference in `PM_ARTIFACTS` identifies a retained source snapshot with a workspace-relative path, such as `releases/COMMIT_SHA/web`.
A client preparation task must restore the same immutable snapshot on every runner before Pulumi runs.
Keep environment files, credentials, state, dependencies, and reports outside that snapshot.
The example rejects those files before uploading source to Vercel.

Target the four database resources before migrations: `*::web-branch`, `*::web-endpoint`, `*::web-role`, and `*::web-database`.
Then run the application's migration task and apply the full program.
Health and rollback remain application tasks. Keep the previous source snapshot available for rollback.

The example retains Vercel deployments as recovery artifacts.
Client cleanup must remove those retained test deployments after destroying a preview.
Do not use the example as an unattended cleanup policy.

This source example is not evidence of a live Neon/Vercel application test.
The published-RC hosted migration and rollback gate remains incomplete until the full application harness and CI credentials are in Premise.
The PostgreSQL/HTTP deployment harness separately proves CLI ordering and automatic recovery locally.
