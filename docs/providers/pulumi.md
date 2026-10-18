# Pulumi state encryption

Pulumi can save secret configuration and outputs in state. Those values need
an encryption provider in addition to credentials for the state storage backend.
GCS encryption at rest does not replace Pulumi's encryption of secret values.

A DIY backend defaults to passphrase encryption. `PULUMI_CONFIG_PASSPHRASE`
unlocks that encryption in non-interactive runs. It is a state encryption
credential, not a GCP login or an application secret. Retain it in protected CI
secrets and operator recovery storage when choosing that provider. The disposable
GCS smoke test uses a passphrase; client workflows do not mandate one.

To use a managed key instead, select Pulumi's native encryption provider:

```sh
pm infra init --env stage \
  --secrets-provider gcpkms://projects/PROJECT/locations/LOCATION/keyRings/RING/cryptoKeys/KEY
```

`pm deploy` accepts the same flag for stacks it creates. The action exposes
`secrets-provider`; generated workflows read `PREMISE_SECRETS_PROVIDER` from CI
variables. The URL is non-secret configuration. An unset value preserves Pulumi's
default. Existing stacks retain their encryption provider.

Supported native choices include `gcpkms`, `awskms`, `azurekeyvault`, and
`hashivault`. Authenticate to the key service and grant encryption/decryption
access. KMS-backed stacks do not require a Pulumi passphrase. Keep their keys
independently of preview resources.

Changing an existing stack's provider is a separate operator migration:
`pulumi stack change-secrets-provider PROVIDER_URL`. It needs access to both the
old and new encryption providers. Premise does not rotate it during deployment.
See [Pulumi secrets handling](https://www.pulumi.com/docs/iac/concepts/secrets/).
