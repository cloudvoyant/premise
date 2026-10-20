# Live application deployment fixture

The client owns `infra/` and imports the published Neon and Vercel providers.
The web application comes from the Premise Bun TanStack Start template.
`template-source.json` records its source commit. The fixture adds a database health route and the Nitro adapter required by Vercel.
It updates TanStack Start to `1.168.60` and Router to `1.170.41` for the [published security fix](https://tanstack.com/blog/tanstack-start-security-update-cve-2026-102989). The dependency lockfile records the patched server core.

## Usage

Run **On Deploy** manually on `feature/pulumi`. Set `validation=cloud`, `version` to the exact published RC, and choose a scenario.
The dedicated workflow can also be dispatched directly after it reaches the default branch.
CI credentials belong to the `preview` environment. Google Cloud uses workload identity; Neon and Vercel use CI secrets.
The fixture uses dedicated Premise projects, never the source application's projects.

```text
client/
├── premise.base.yaml       Environment and scenario configuration
├── premise.yaml            Initial configuration; preparation selects the scenario
├── mise.toml               Tool pins and preparation
├── tasks/main.go           Application migrations, health, recovery, assertions
├── apps/
│   ├── api/mise.toml       Optional migration task
│   ├── worker/mise.toml    Optional migration task
│   └── web/                Actual TanStack Start application
└── infra/
    ├── Pulumi.yaml         Go runtime and published Neon SDK generation
    ├── main.go             Database and application composition
    ├── database.go         Branch, endpoint, role, database, secret URL
    └── application.go      Vercel deployment and environment alias
```

Each run deploys A, then attempts B from another set of runners.
Preparation restores both immutable source snapshots from the checked-out RC.
Each snapshot contains a different compiled source marker, A or B. Health checks verify that marker as well as the runtime release identity, so changing an environment variable alone cannot pass rollback validation.
Only application tasks request secret database outputs with `pm infra query --show-secrets`.
Database URLs never enter release records or source snapshots.

| Scenario         | Expected result                                                               |
| ---------------- | ----------------------------------------------------------------------------- |
| `parallel`       | Two migration jobs meet at a remote barrier before the full update            |
| `shared`         | One job migrates API then worker against shared history                       |
| `none`           | No migration jobs; the application sees uninitialized tables                  |
| `fail-migration` | B migration fails; full application update is skipped; A keeps serving        |
| `fail-apply`     | B database migrations succeed; provider apply fails; recovery keeps A serving |
| `rollback`       | B serves real data, its health task fails, and automatic recovery restores A  |

Assertions query the serving application, PostgreSQL migration history and schema, and durable deployment records.
The original B run must remain failed in failure scenarios. A must remain the last successful release.
Compatible B schema changes remain after recovery. Clients own migration compatibility.

Cleanup destroys the owned preview and removes retained Vercel deployments tagged with that exact stack.
The remote state bucket, dedicated parent projects, and other previews remain intact.
The CI run is evidence only when its assertions and cleanup pass; fixture source alone does not prove live behavior.
