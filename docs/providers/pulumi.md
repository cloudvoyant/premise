# Pulumi state protection

## Usage

Premise currently supplies an empty Pulumi passphrase for every invocation. This needs no extra CI secret. Custom passphrases and encryption-provider flags are outside the current interface.

Backend access controls, transport security, and storage encryption protect state. An empty passphrase gives no meaningful protection to someone who can read the state. Keep backend access separate from application access.

Do not adopt an existing stack that requires a nonempty passphrase through this interface. Changing a stack's encryption provider requires a separate operator migration with access to the original key. Premise does not rotate keys during deployment.

Managed encryption is tracked in [DIFF-232](https://linear.app/cloudvoyant/issue/DIFF-232/replace-the-empty-pulumi-passphrase-with-managed-state-encryption). Passphrase customization is tracked separately in [DIFF-235](https://linear.app/cloudvoyant/issue/DIFF-235/support-explicit-pulumi-passphrases-after-the-state-encryption-design). See [Pulumi secrets handling](https://www.pulumi.com/docs/iac/concepts/secrets/).
