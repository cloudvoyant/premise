# AWS

## Tools

Declare Pulumi through Mise. Add `awscli` when the root authentication task uses it. Declare `aws` under `workspace.infra.providers` to require the fallback setup.

## Local authentication

Use your organization’s AWS SSO profile:

```sh
aws sso login --profile development
AWS_PROFILE=development pm infra plan --env dev
```

Pulumi uses the AWS SDK credential chain. Premise does not implement native AWS login.

## CI authentication

Supply a trusted web-identity role or temporary credentials from the runner. Declare `ci:auth` in the root `mise.toml` for the AWS setup. It runs after native providers.

```toml
[tasks."ci:auth"]
run = "aws sts get-caller-identity >/dev/null"
```

```sh
pm infra auth --ci
```

The identity check uses the runner’s standard variables. Add the identity exchange to that task when the runner does not prepare credentials.

## Variables

| Name                                                              | Source                | Purpose                                                 |
| ----------------------------------------------------------------- | --------------------- | ------------------------------------------------------- |
| `AWS_ROLE_ARN`, `AWS_WEB_IDENTITY_TOKEN_FILE`                     | Runner identity setup | Role and token file for web identity.                   |
| `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | CI identity exchange  | Alternative temporary credentials.                      |
| `AWS_REGION`                                                      | CI variable           | Region for project resources.                           |
| `PULUMI_BACKEND_URL`                                              | CI variable           | Existing state store, for example `s3://company-state`. |

## Permissions

Grant resource rights separately from S3 state permissions. A state bucket in another account does not select the account for deployed resources. Preview cleanup needs the same resource and state access as creation.

## References

- [Pulumi AWS configuration](https://www.pulumi.com/registry/packages/aws/installation-configuration/)
- [Pulumi backend authentication](https://www.pulumi.com/docs/iac/operations/stack-management/using-a-diy-backend/)
