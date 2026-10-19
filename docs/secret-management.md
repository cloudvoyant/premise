# Secret management

An application secret is a value that the application needs. A deployment credential grants access to change infrastructure. Premise keeps these two groups separate.

## Ownership

| Value                                      | Source                                                                   | Consumer                                   |
| ------------------------------------------ | ------------------------------------------------------------------------ | ------------------------------------------ |
| Stored application secrets                 | GCP Secret Manager through fnox                                          | Local application and local migration task |
| Database connection for a preview          | Secret output from its Pulumi stack                                      | Migration task and server-side application |
| Neon and Vercel administration credentials | Provider-supported OAuth or CI secrets                                   | Declared deployment and recovery tasks     |
| GCP authentication                         | Local Application Default Credentials or CI workload identity            | fnox, Pulumi and durable record storage    |
| Pulumi state protection                    | Backend access controls and storage encryption; empty passphrase for now | Pulumi configuration and state             |
| Project IDs and environment labels         | Committed non-secret configuration                                       | Environment resolver                       |

Vercel receives application values during deployment. It does not own the source values for `pm secrets pull`. Do not put deployment credentials in an application fnox profile. Do not put database credentials in a browser bundle.

## Configure application secrets

Premise uses fnox 1.39.0 through Mise. Its `gcp-sm` provider reads GCP Secret Manager. This provider is separate from the GCS bucket that holds Pulumi state.

Declare keys and profiles on the application project in `premise.yaml`:

```yaml
workspace:
  projects:
    - name: web
      template: cloudvoyant/premise-bun:premise-tanstack-start-app
      path: apps/web
      secrets:
        config: fnox.toml
        profiles:
          dev: dev
          stage: stage
          prod: prod
          preview: preview
        keys: [APP_SECRET]
        file: .env.local
```

Create `apps/web/fnox.toml` with references:

```toml
[providers.gcp]
type = "gcp-sm"
project = "your-gcp-project"
prefix = "web-"

[profiles.dev.secrets]
APP_SECRET = { provider = "gcp", value = "dev-app-secret" }

[profiles.stage.secrets]
APP_SECRET = { provider = "gcp", value = "stage-app-secret" }

[profiles.prod.secrets]
APP_SECRET = { provider = "gcp", value = "prod-app-secret" }

[profiles.preview.secrets]
APP_SECRET = { provider = "gcp", value = "preview-app-secret" }
```

Store the values in Secret Manager. Grant access only to the required secrets. The configuration contains secret names, not secret values. Premise requires an explicit profile and declared keys. It does not fall back to production or top-level fnox secrets.

## Local use

If you use GCP Secret Manager locally, configure Application Default Credentials:

```sh
gcloud auth application-default login
```

Add `.env.local` to the application's Git ignore rules. Then pull its application values:

```sh
pm secrets pull --env dev --project web
```

The command writes `apps/web/.env.local` with permissions `0600`. These permissions allow only your user to read and write it. Premise resolves all values before it replaces the file. It keeps the existing file if secret resolution fails.

If you omit `--project`, Premise pulls every declared application destination. If you select `preview`, Premise resolves the current feature branch. A profile can contain `{branch}` to select its stable preview identity. That explicit profile must exist in the fnox configuration.

Use child-process injection when a task does not need a file:

```sh
pm secrets exec --env dev --project web -- mise run migrate
```

Premise removes common administration tokens from the child environment. It calls `fnox env --json --keys ...` once for the project's declared keys. Fnox owns provider access, interpolation, and profile resolution. It masks these values in captured child output. Do not print transformed secrets or enable shell tracing.

Premise does not execute env-file text as shell code. It quotes values and rejects empty values, invalid UTF-8 and NUL characters. Generated env files must remain outside deployment bundles, caches and reports.

## Infra program secrets

The infrastructure program declares its own `secrets` keys using the same configuration as an app. Local use resolves its fnox profile. CI supplies its declared variables directly. Its keys arrive as ordinary process environment variables. The Go program reads those variables and assigns them to the intended component, wrapping provider secret inputs with `pulumi.ToSecret`. For several apps, declare distinct names such as `WEB_SECRET` and `ADMIN_SECRET`. Bindings carry source paths only.

Premise uses fnox's versioned environment API because it starts Pulumi and task subprocesses itself. The scalar adapter keeps the explicit environment selection, ignored atomic `0600` files, administration-token filtering, and output masking. It rejects file secrets and credential leases; use native `fnox exec` for those contracts. It also refuses inherited values that fnox requires removing. See [fnox environment API](https://fnox.jdx.dev/reference/env-json.html).

## CI use

Keep fnox secret providers local. Copy the selected application values into CI secrets from your workstation:

```sh
pm secrets sync --env stage --project web --ci github --repository owner/repo
pm secrets sync --env stage --project web --ci gitlab --repository group/repo
```

Authenticate `gh` or `glab` locally before the sync. Declare these tools through Mise. The destination environment must exist. GitHub receives environment secrets. GitLab receives masked, raw variables scoped to that environment. GitLab masking rules can reject short or multiline values. The command reports the failed key without printing its value. Earlier keys can already be updated when a later key fails.

The command reads the local fnox profile. That profile can use GCP Secret Manager, a local env file, or another fnox provider. Secret values travel to the CI CLI on standard input. They do not appear in command arguments or logs. Repeat the sync after rotation.

Map each declared key into its job environment:

```yaml
env:
  APP_SECRET: ${{ secrets.APP_SECRET }}
```

In CI, `pm secrets exec`, migration tasks, and Pulumi program secrets read their declared keys directly from the environment. They do not invoke fnox or contact its secret provider. A missing key stops the operation. CI must set `CI=true`, `GITHUB_ACTIONS=true`, or `GITLAB_CI=true`.

Configure deployment credentials separately through [provider setup](providers/README.md). OAuth or protected CI administration tokens authenticate the infrastructure operation. `pm infra auth --ci` runs native authentication before the optional root `ci:auth` fallback. Provider guides define the variable names. The empty Pulumi passphrase requires no additional CI secret.

## Rotation and previews

A pull retrieves values again through fnox without daemon caching. Rotate local source values, then repeat the pull or process execution. Sync them again before the next CI deployment. Environment files are local snapshots. Remove an obsolete snapshot rather than using it as the source for a new secret.

Preview database connections come from the selected branch stack. Templates can publish these outputs to branch-specific Secret Manager entries when a persistent local pull needs them. The template owns this publication contract. Cleanup removes only entries that the preview owns.

Recovery records contain release identities and references. They do not contain secret values. Do not restore revoked credentials when you restore an application release. Database migration compatibility remains the client's responsibility.

## Failures

If a profile or secret is missing, Premise stops before writing an env file. Error messages identify the missing key or profile. Provider diagnostics are suppressed because they can contain credentials.

If access fails, make sure that Application Default Credentials exist and the identity can access the selected secret. A gcloud CLI login alone does not establish this access. If a child command fails, Premise reports the failure and masks resolved values in its captured output.

## References

- [fnox GCP Secret Manager](https://fnox.jdx.dev/providers/gcp-sm.html)
- [Mise fnox integration](https://mise.jdx.dev/environments/secrets/fnox.html)
- [GCP Secret Manager authentication](https://docs.cloud.google.com/secret-manager/docs/authentication)
- [Infrastructure deployment](modules/infrastructure.md)
