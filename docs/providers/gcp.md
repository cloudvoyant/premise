# Google Cloud

## Tools

Declare GCP authentication separately from Pulumi resources:

```yaml
workspace:
  infra:
    providers: [gcp]
```

Install tools through the workspace `mise.toml`:

```toml
[tools]
gcloud = "559.0.0"
pulumi = "3.268.0"
```

Run `mise install` before authentication.

## Local authentication

```sh
pm infra auth
```

Premise runs `gcloud auth application-default login` for Pulumi and `gcloud auth login` for gcloud commands. Application Default Credentials (ADC) are the SDK credential files. These interactive commands use OAuth.

Select resource projects and regions in normal Pulumi configuration. A state bucket can belong to another project.

## CI authentication

Configure workload identity federation for the CI issuer, repository, and allowed branches. Grant the CI identity permission to impersonate its deployment service account.

```sh
pm infra auth --ci --env-file "$GITHUB_ENV"
```

Keep provider configuration in the root Mise environment, rather than provider-specific workflow YAML. Federation identifiers are configuration, not credentials:

```toml
[env]
GCP_WORKLOAD_IDENTITY_PROVIDER = "projects/NUMBER/locations/global/workloadIdentityPools/POOL/providers/PROVIDER"
GCP_SERVICE_ACCOUNT = "deploy@PROJECT.iam.gserviceaccount.com"
```

The generic workflow runs `mise exec -- pm infra auth --ci --env-file "$GITHUB_ENV"`. Mise supplies the provider configuration. OAuth supplies the short-lived deployment credentials. For an existing credential file, supply `GOOGLE_APPLICATION_CREDENTIALS` through the runner environment or a private Mise env file. Never commit credential contents.

Premise accepts existing ADC or creates credentials from the runner identity. On GitHub, it requests the runner's identity token directly. No Google auth action is required. Grant the job `id-token: write`. Other CI systems supply `CI_OIDC_TOKEN_FILE` with the configured audience.

Premise calls `gcloud iam workload-identity-pools create-cred-config`. It writes private temporary files and returns the two standard credential paths. Keep these files outside source control, caches, and artifacts. Remove them when the job finishes.

## Variables

| Name | Source | Purpose |
| --- | --- | --- |
| `GCP_WORKLOAD_IDENTITY_PROVIDER` | Root Mise environment | Federation provider path: `projects/NUMBER/locations/global/workloadIdentityPools/POOL/providers/PROVIDER`. |
| `GCP_SERVICE_ACCOUNT` | Root Mise environment | Deployment service-account email. |
| `CI_OIDC_TOKEN_FILE` | Runner | Identity token file for runners other than GitHub. |
| `GOOGLE_APPLICATION_CREDENTIALS` | Existing runner credentials or Premise auth output | ADC file for Pulumi. |
| `CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE` | Premise auth output | Same ADC file for gcloud. |
| `PULUMI_BACKEND_URL` | Repository backend configuration or environment | Existing state bucket URL, for example `gs://company-state`. |

Existing `GOOGLE_APPLICATION_CREDENTIALS` takes precedence over federation inputs. Premise checks that it names a file. Pulumi and gcloud make the authenticated requests and report permission errors.

GitHub exposes `ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN` to jobs with identity-token permission. Premise uses these runner variables only for the identity exchange. Never copy their values into CI secrets.

## Permissions

Enable APIs for the client-owned resources. Grant create, update, and delete access in their destination projects. Grant object read, write, and delete access plus bucket metadata access on the existing state bucket.

Local env generation and E2E credential tasks using fnox require Secret Manager access. Grant their selected identities access to the referenced documents. Client Pulumi programs own remote application secret bindings. Preview cleanup requires the same deployment rights as creation. Preserve the state bucket and project after cleanup.

## References

- [GCP credential configuration](https://docs.cloud.google.com/sdk/gcloud/reference/iam/workload-identity-pools/create-cred-config)
- [GitHub identity-token variables](https://docs.github.com/en/actions/reference/security/oidc)
- [Pulumi GCP configuration](https://www.pulumi.com/registry/packages/gcp/installation-configuration/)
