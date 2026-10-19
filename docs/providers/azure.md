# Azure

## Tools

Declare Pulumi through Mise. Add the Azure CLI when authentication tasks use it. Declare `azure` under `workspace.infra.providers` to require the fallback setup.

## Local authentication

```sh
az login
pm infra plan --env dev
```

Use the published provider’s supported Azure login flow. Premise does not implement native Azure login.

## CI authentication

Configure a federated identity for the repository and environment. Supply the runner identity through the Azure Native provider’s standard variables. Declare `ci:auth` in the root `mise.toml` to prepare or check these credentials. It runs after native providers.

```toml
[tasks."ci:auth"]
run = './scripts/azure-auth.sh'
```

```sh
pm infra auth --ci
```

The script owns the Azure-specific identity setup. It must fail if authentication fails.

## Variables

| Name                                                    | Source                    | Purpose                                                     |
| ------------------------------------------------------- | ------------------------- | ----------------------------------------------------------- |
| `ARM_TENANT_ID`, `ARM_CLIENT_ID`, `ARM_SUBSCRIPTION_ID` | CI variables              | Azure Native deployment identity and subscription.          |
| `ARM_USE_OIDC`                                          | CI variable set to `true` | Select federated identity.                                  |
| `ARM_OIDC_TOKEN` or `ARM_OIDC_TOKEN_FILE_PATH`          | Runner                    | Identity token or its file path.                            |
| `PULUMI_BACKEND_URL`                                    | CI variable               | Existing state store, for example `azblob://company-state`. |

## Permissions

Grant resource permissions in the intended subscription. Grant backend storage access separately. Azure Blob state uses the Azure SDK credential chain, which differs from the provider’s `ARM_*` variables. Configure both consumers. Preview cleanup needs the same rights as creation.

## References

- [Pulumi Azure Native configuration](https://www.pulumi.com/registry/packages/azure-native/installation-configuration/)
- [Pulumi backend authentication](https://www.pulumi.com/docs/iac/operations/stack-management/using-a-diy-backend/)
