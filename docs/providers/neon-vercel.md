# Neon and Vercel

The tandem templates use published providers. The Neon provider reads
`NEON_API_KEY`; the Vercel provider reads `VERCEL_API_TOKEN`. These are
administration credentials, distinct from database connections or application
secrets. See the [Neon](https://www.pulumi.com/registry/packages/neon/api-docs/provider/)
and [Vercel](https://www.pulumi.com/registry/packages/vercel/api-docs/provider/)
provider references.

Supply these tokens as protected CI secrets when using token authentication.
Map them explicitly to the deployment and cleanup jobs that need them. For
example, in a client workflow that uses these providers:

```yaml
env:
  NEON_API_KEY: ${{ secrets.NEON_API_KEY }}
  VERCEL_API_TOKEN: ${{ secrets.VERCEL_TOKEN }}
```

Scope the credentials to the intended test or deployment accounts. Select Neon
project and Vercel team/project IDs in the client Pulumi program or configuration.
An interactive CLI login does not establish the provider's token contract.
Premise's generic workflow does not assume these providers or include their
secret mappings.

Keep these tokens out of application fnox profiles, local application env files,
and frontend bundles. Migration and health tasks use application credentials.
Cleanup needs the same administration access as creation, even after a branch
merges or is deleted.
