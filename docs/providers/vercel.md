# Vercel

## Tools

Declare the provider in `premise.yaml`:

```yaml
workspace:
  infra:
    providers: [vercel]
```

Declare Pulumi in `mise.toml`. The Go component uses its published provider package. A separate Vercel CLI is not required.

```toml
[tools]
pulumi = "3.268.0"
```

## Local authentication

The Pulumi provider uses `VERCEL_API_TOKEN`. An interactive CLI login does not establish this environment variable. Keep administration tokens in CI secrets under the current credential policy. Use CI for deployments that require this token. Client code owns its provider dependencies and resource declarations.

## CI authentication

Create a protected CI secret named `VERCEL_API_TOKEN`. Map it into deployment and cleanup jobs:

```yaml
env:
  VERCEL_API_TOKEN: ${{ secrets.VERCEL_API_TOKEN }}
```

```sh
pm infra auth --ci
pm infra plan --env preview --branch feature/example
```

Premise checks that the variable exists before infrastructure work. Pulumi uses the token directly. Do not put it in application fnox profiles or application env files.

## Variables

| Name                 | Source              | Purpose                                                  |
| -------------------- | ------------------- | -------------------------------------------------------- |
| `VERCEL_API_TOKEN`   | Protected CI secret | Administration access for the published Vercel provider. |
| `PULUMI_BACKEND_URL` | CI variable         | Existing remote state store, authenticated separately.   |

Select resource IDs and account configuration in the client-owned Pulumi program under `infra/`. Import the published provider directly.

## Permissions

Scope the token to the intended Vercel team and projects. Grant project, deployment, environment, and alias access needed by the component. Cleanup needs the same access after a merge or branch deletion. Application credentials and database connections belong to application tasks, separate from administration access.

## References

- [Published Vercel provider](https://www.pulumi.com/registry/packages/vercel/api-docs/provider/)
- [Secret management](../secret-management.md)
