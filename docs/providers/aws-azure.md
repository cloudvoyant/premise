# AWS and Azure

Use the published Pulumi provider's standard credential chain. Premise adds no
cloud-specific login commands. Declare `awscli` or `azure` in Mise only when
project tasks need those CLIs; provider SDKs do not require an extra CLI install.

## AWS

Locally, use an AWS profile or your organization's SSO flow. In CI, use a trusted
OIDC role and supply its web identity token file through the AWS SDK credential
chain, or short-lived `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and
`AWS_SESSION_TOKEN` values from the runner's identity exchange. Configure region
and role selection using the provider's standard settings.

Give the identity resource permissions separately from S3 state object and KMS
key permissions. A cross-account state bucket does not select the account where
Pulumi deploys resources. See [Pulumi AWS configuration](https://www.pulumi.com/registry/packages/aws/installation-configuration/).

## Azure

Locally, use your organization's Azure login flow. For the Azure Native provider
in CI, configure `ARM_TENANT_ID`, `ARM_CLIENT_ID`, `ARM_SUBSCRIPTION_ID`, and
`ARM_USE_OIDC=true`, with the runner's token in `ARM_OIDC_TOKEN` or the file named
by `ARM_OIDC_TOKEN_FILE_PATH`. Restrict the federated identity to the intended
repository and environment. See [Azure Native configuration](https://www.pulumi.com/registry/packages/azure-native/installation-configuration/).

The Azure Blob state backend uses the Azure SDK credential chain, which differs
from the Pulumi provider's `ARM_*` settings. Configure backend credentials
separately; do not assume provider authentication also authenticates storage.
See [Pulumi DIY backends](https://www.pulumi.com/docs/iac/operations/stack-management/using-a-diy-backend/).
