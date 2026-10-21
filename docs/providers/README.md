# Provider setup

Declare the cloud services that your infrastructure uses. Pulumi still owns resources and provider configuration.

```yaml
workspace:
  infra:
    providers: [gcp]
```

## Usage

Install tools through Mise, then authenticate:

```sh
mise install
pm infra auth
pm infra init --env dev
```

In CI, use the runner identity or protected CI secrets:

```sh
pm infra auth --ci --env-file "$GITHUB_ENV"  # GitHub environment file
pm deploy run --env stage --release COMMIT_SHA --artifact web=ARTIFACT_ID
```

`--env-file` appends credential file paths for later jobs steps. It never writes token values. Other runners can pass the returned paths through their own environment mechanism. Existing standard credential files also work.

Premise supports native GCP authentication. For unsupported providers, declare `ci:auth` in the root `mise.toml`. Premise runs native authentication first, then this optional fallback. Project or global Mise tasks cannot supply the fallback. A failed native operation or fallback stops the job.

```toml
[tasks."ci:auth"]
run = "./scripts/custom-provider-auth.sh"
```

The fallback can create credential files at paths already defined in CI. A child task cannot export variables to its parent.

## Credentials and application values

Keep fnox providers local. Run `pm secrets sync` locally to copy selected application values into CI secrets. CI reads those values from its environment and does not contact fnox or Secret Manager. See [Secret management](../secret-management.md).

Deployment credentials come from OAuth or protected CI secrets. They do not belong in application fnox profiles. State storage and deployed resources can require separate permissions. Backend access controls protect state while the empty Pulumi passphrase remains the default.

Deployment records use conditional writes in an existing S3 or GCS prefix.
The default prefix lives beside the selected Pulumi state. Set `deployment.records` to choose another prefix.
Install AWS CLI or gcloud through Mise for that store. Each CI job authenticates independently.
The action does not copy runner-local record directories between jobs.

## Providers

| Provider     | Native authentication                             | Guide             |
| ------------ | ------------------------------------------------- | ----------------- |
| Google Cloud | Local OAuth, CI workload identity or existing ADC | [GCP](gcp.md)     |
| AWS          | Runner credentials or root fallback               | [AWS](aws.md)     |
| Azure        | Runner credentials or root fallback               | [Azure](azure.md) |

See [Pulumi state protection](pulumi.md) for the current empty-passphrase decision and encryption follow-up.
