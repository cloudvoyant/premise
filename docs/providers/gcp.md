# Google Cloud

## Tools

Declare the CLI in the workspace's `mise.toml` when tasks use it:

```toml
[tools]
gcloud = "559.0.0"
pulumi = "3.268.0"
```

Run `mise install`. Installing gcloud does not authenticate it.

## Local authentication

Use `gcloud auth application-default login` for Pulumi and fnox. This creates
Application Default Credentials (ADC). Commands that use gcloud itself can also
need `gcloud auth login`. Select the resource project in normal Pulumi provider
configuration; a state bucket can belong to a different project.

## CI authentication

Configure workload identity federation for the CI issuer, repository, and allowed
branches/environments. Give that identity permission to impersonate the deployment
service account when using service-account impersonation. Supply ADC through
`GOOGLE_APPLICATION_CREDENTIALS`, pointing to an external-account credential file.
For gcloud commands, set `CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE` to the same file.
Keep the credential and its source token file outside source control and caches.

If the runner supplies an OIDC token file, a workspace `ci:auth` task can create
that ADC configuration:

```toml
[tasks."ci:auth"]
run = '''
gcloud iam workload-identity-pools create-cred-config "$GCP_WIF_PROVIDER" \
  --service-account="$GCP_SERVICE_ACCOUNT" \
  --credential-source-file="$CI_OIDC_TOKEN_FILE" \
  --output-file="$GOOGLE_APPLICATION_CREDENTIALS"
'''
```

The runner supplies `CI_OIDC_TOKEN_FILE`. Its token audience must match the
configured federation provider. Set both credential path variables in CI or Mise
before the task runs; use a private temporary directory. `GCP_WIF_PROVIDER` and
`GCP_SERVICE_ACCOUNT` are non-secret configuration. The task creates an identity
configuration, not a service-account key. See Google's
[credential configuration command](https://docs.cloud.google.com/sdk/gcloud/reference/iam/workload-identity-pools/create-cred-config).

A CI-native OIDC adapter may provide these files and variables directly. The
GCS smoke workflow uses this approach with `google-github-actions/auth`; Mise
installs gcloud. Neither the generic deploy workflow nor Premise's Go code
requires that adapter.

## Permissions

Enable APIs for the resources the template creates. Grant resource creation,
update, and deletion rights in their destination projects. For the GCS backend,
grant object read/write/delete and bucket metadata access on the existing state
bucket. Deployment recovery record synchronization needs object read/write/list
access on its configured GCS prefix.

For fnox, enable Secret Manager and grant access to the referenced secret versions.
Only a template that creates secrets needs secret creation/deletion permissions.
For `gcpkms://` state encryption, separately grant encryption/decryption access to
the selected KMS key. Keep the key and state backend after preview destruction.

See [Pulumi GCP setup](https://www.pulumi.com/registry/packages/gcp/installation-configuration/)
and [GCS state authentication](https://www.pulumi.com/docs/iac/operations/stack-management/using-a-diy-backend/).
