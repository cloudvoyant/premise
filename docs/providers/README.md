# Provider setup

Declare the cloud services that your infrastructure uses. Pulumi still owns resources and provider configuration.

```yaml
workspace:
  infra:
    providers: [gcp]
```

## Usage

Install tools through Mise, then authenticate. CI also calls `pm infra auth --ci`; do not use cloud-provider GitHub Actions for authentication or tool setup:

```sh
mise install
pm infra auth
pm infra init --env dev
```

In CI, use the runner identity or protected CI secrets:

```sh
pm infra auth --ci --env-file "$GITHUB_ENV"  # GitHub environment file
pm deploy --env stage --release COMMIT_SHA
```

`--env-file` appends credential file paths for later jobs steps. It never writes token values. Other runners can pass the returned paths through their own environment mechanism. Existing standard credential files also work.

Premise supports native GCP authentication. For unsupported providers, declare `ci:auth` in the root `mise.toml`. Premise runs native authentication first, then this optional fallback. Project or global Mise tasks cannot supply the fallback. A failed native operation or fallback stops the job.

```toml
[tasks."ci:auth"]
run = "./scripts/custom-provider-auth.sh"
```

The fallback can create credential files at paths already defined in CI. A child task cannot export variables to its parent.

## Credentials and application values

Local env files come from fnox and project Mise tasks. Client Pulumi code configures deployed application secrets. Premise does not copy application values into CI secrets. See [Secret management](../guides/secrets-and-config.md).

Deployment credentials come from OAuth, workload identity, or protected CI credentials. Pulumi owns backend access, state locking, and update history. Premise has no separate recovery store. Hosted CI concurrency serializes each environment's deployment steps.

## Providers

| Provider     | Native authentication                             | Guide             |
| ------------ | ------------------------------------------------- | ----------------- |
| Google Cloud | Local OAuth, CI workload identity or existing ADC | [GCP](gcp.md)     |
| Azure        | Runner credentials or root fallback               | [Azure](azure.md) |

See [Pulumi state protection](pulumi.md) for the current empty-passphrase decision and encryption follow-up.
