# Neon

## Tools

Declare the provider in `premise.yaml`:

```yaml
workspace:
  infra:
    providers: [neon]
```

Declare Pulumi in `mise.toml`. The Go component uses its published provider package. A separate Neon CLI is not required.

```toml
[tools]
pulumi = "3.268.0"
```

## Local authentication

The Pulumi provider uses `NEON_API_KEY`. An interactive CLI login does not establish this environment variable. Keep administration tokens in CI secrets under the current credential policy. Use CI for deployments that require this token. Local component tests can use Pulumi mocks without deployment credentials.

```sh
pm infra test
```

## CI authentication

Create a protected CI secret named `NEON_API_KEY`. Map it into deployment and cleanup jobs:

```yaml
env:
  NEON_API_KEY: ${{ secrets.NEON_API_KEY }}
```

```sh
pm infra auth --ci
pm infra plan --env preview --branch feature/example
```

Premise checks that the variable exists before infrastructure work. Pulumi uses the token directly. Do not put it in application fnox profiles or application env files.

## Variables

| Name                 | Source              | Purpose                                                |
| -------------------- | ------------------- | ------------------------------------------------------ |
| `NEON_API_KEY`       | Protected CI secret | Administration access for the published Neon provider. |
| `PULUMI_BACKEND_URL` | CI variable         | Existing remote state store, authenticated separately. |

Select resource IDs and account configuration in the client Pulumi program. The separate `neon-db` template defines the project resources. It does not implement a provider.

## Permissions

Scope the token to the intended Neon organization and projects. Grant access to branches, endpoints, roles, and databases used by the component. Cleanup needs the same access after a merge or branch deletion. Application credentials and database connections belong to application tasks, separate from administration access.

## References

- [Published Neon provider](https://www.pulumi.com/registry/packages/neon/api-docs/provider/)
- [Secret management](../secret-management.md)
