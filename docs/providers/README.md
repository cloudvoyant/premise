# Provider setup

Premise runs Pulumi and project Mise tasks with the credentials provided by the
caller. It does not log into cloud accounts or translate provider credentials.
The same contract applies locally, in GitHub Actions, and in other CI systems.

## Tools and credentials

Declare Pulumi and any required provider CLIs in workspace or project
`mise.toml` files. Mise installs these tools before deployment. The generated
workflow does not install gcloud through a separate action or assume GCP,
Neon, or Vercel credentials.

Provide the provider's standard environment variables or credential files before
running `pm infra` or `pm deploy`. Prefer short-lived OAuth or workload identity.
If a provider requires an administration token, map a protected CI secret to its
standard environment variable. Application fnox profiles must not contain those
tokens. See [Secret management](../secret-management.md).

A workspace can declare a `ci:auth` Mise task to prepare credential files from the
CI runner's identity. Generated deploy and cleanup jobs run this task after Mise
setup, if it exists. It must fail when authentication fails. An absent task means
the runner already supplies credentials. A child task cannot export variables to
its parent: configure paths in the job environment or Mise environment settings
so later processes can find its files. Use the same configuration for deploy and
cleanup, and never cache or upload credential files.

The CI adapter only obtains the runner's identity and exposes standard provider
credentials. For example, a GitHub OIDC action can exchange that identity for a
credential file; another runner can supply its own token file. Provider tools and
Pulumi programs remain in Mise and do not depend on that action.

## Separate access requirements

State storage, infrastructure resources, application secrets, and state encryption
can use different services or accounts. Grant each identity access to the resources
it needs. A state bucket grant does not grant permission to create infrastructure
or decrypt Pulumi secrets.

The current Premise action syncs deployment recovery records using `gcloud storage`.
When using that action's `deploy` operation, declare gcloud in Mise and supply GCP
credentials plus a GCS `records-url` (or a GCS state backend). This does not restrict
the providers in the Pulumi program. For another record store, call the CLI and
restore/persist its record directory through your own CI tasks.

## Providers

- [Google Cloud](gcp.md): ADC, workload identity, state, and Secret Manager access.
- [AWS and Azure](aws-azure.md): their SDK credential chains and backend differences.
- [Neon and Vercel](neon-vercel.md): administration tokens in protected CI.
- [Pulumi state encryption](pulumi.md): passphrases and managed encryption keys.

Preview cleanup runs when a same-repository PR merges into `main` and when a
feature branch is deleted. Allow the trusted default branch to use the CI preview
environment for cleanup. Push, merge, and deletion use the same branch concurrency
group. A missing preview stack is already clean; authorization
and backend errors still fail the job.
