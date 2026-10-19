# Pulumi state encryption

Premise currently uses an empty passphrase when the caller supplies neither
`PULUMI_CONFIG_PASSPHRASE` nor `PULUMI_CONFIG_PASSPHRASE_FILE`. This requires no
additional CI secret or local setup. The GCS smoke test uses the same empty
passphrase explicitly.

Pulumi still encodes secret values as encrypted state, but an empty passphrase
provides no meaningful protection to someone who can read that state. For now,
protection comes from the backend's access controls, transport security, and
storage encryption. Stronger state encryption is deferred to the backend project.

An explicit passphrase or passphrase-file setting takes precedence. Existing
stacks encrypted with a non-empty passphrase still require their original key;
this default does not migrate them.

To use a managed key instead, select Pulumi's native encryption provider:

```sh
pm infra init --env stage \
  --secrets-provider gcpkms://projects/PROJECT/locations/LOCATION/keyRings/RING/cryptoKeys/KEY
```

`pm deploy` accepts the same flag for stacks it creates. The action exposes
`secrets-provider`; generated workflows read `PREMISE_SECRETS_PROVIDER` from CI
variables. The URL is non-secret configuration. An unset value uses the current
empty-passphrase default for new stacks. Existing stacks retain their encryption
provider.

Supported native choices include `gcpkms`, `awskms`, `azurekeyvault`, and
`hashivault`. Authenticate to the key service and grant encryption/decryption
access. KMS-backed stacks do not require a Pulumi passphrase. Keep their keys
independently of preview resources.

Changing an existing stack's provider is a separate operator migration:
`pulumi stack change-secrets-provider PROVIDER_URL`. It needs access to both the
old and new encryption providers. Premise does not rotate it during deployment.
See [Pulumi secrets handling](https://www.pulumi.com/docs/iac/concepts/secrets/).
