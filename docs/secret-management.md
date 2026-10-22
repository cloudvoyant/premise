# Secret management

Fnox configuration owns local secret references. A project Mise task combines those values with saved infrastructure outputs and writes the env files that local runs need.

Pulumi supplies secrets to deployed applications through their cloud resources. Premise does not fetch application secrets during deployment or copy them into CI secrets. E2E tasks can use fnox for fixture and cleanup credentials.

## Usage

Pin the tools in the root `mise.toml`:

```toml
[tools]
fnox = "1.39.0"
jq = "1.8.1"
```

Projects with a `secrets:pull` task must include `fnox.toml`. There is no secrets configuration in `premise.yaml`. The selected environment names the fnox profile: `dev`, `stage`, `prod`, or `preview`.

For example, `apps/web/fnox.toml` can reference JSON documents in GCP Secret Manager:

```toml
[providers.gcp]
type = "gcp-sm"
project = "my-project"

[profiles.dev.secrets]
APP_CONFIG = { provider = "gcp", value = "web-dev", as_file = true }

[profiles.stage.secrets]
APP_CONFIG = { provider = "gcp", value = "web-stage", as_file = true }

[profiles.prod.secrets]
APP_CONFIG = { provider = "gcp", value = "web-prod", as_file = true }

[profiles.preview.secrets]
APP_CONFIG = { provider = "gcp", value = "web-preview", as_file = true }
```

Each document contains JSON such as `{"message":"hello"}`. Fnox retrieves the selected document and supplies its temporary filename as `APP_CONFIG`. Commit references only. Store real values in Secret Manager.

Define a Bash task in the project's `mise.toml`:

```toml
[tasks."secrets:pull"]
shell = "bash -c"
run = '''
set -euo pipefail
umask 077
git check-ignore --quiet -- .env.local
scope=(--env "$PM_ENV")
if [[ "$PM_ENV" == preview ]]; then scope+=(--branch "$PM_BRANCH"); fi
bucket="$(pm infra query "${scope[@]}" --output bucketName)"
temporary="$(mktemp .env.local.XXXXXX)"
trap 'rm -f "$temporary"' EXIT
jq -r --arg bucket "$bucket" '
  "APP_MESSAGE=" + (.message | @json),
  "BUCKET_NAME=" + ($bucket | @json)
' "$APP_CONFIG" > "$temporary"
mv "$temporary" .env.local
'''
```

The task owns the output filename and encoding. This example uses JSON quoting for its dotenv strings. Use the consuming application's dotenv encoder when its rules differ. The temporary file preserves the previous env file if the query or JSON conversion fails.

Ignore `.env.local` and its `.env.local.*` temporary files, then run:

```sh
pm secrets pull --project web --env dev
pm secrets pull --project web --env preview --branch feature/example
```

Premise sets `PM_ENV` and `PM_BRANCH`, selects the matching fnox profile, and runs the task. Omit `--project` to run all declared project tasks with that name. Projects without the task are skipped.

The application or Mise loads the generated file for local runs. There is no public `pm secrets exec` or `pm secrets sync` command.

## Deployment and CI

Client Pulumi programs obtain application secrets from the chosen secret provider and configure the deployed resources. For example, a client can bind a Secret Manager reference to a service. This is client infrastructure code.

Deployment credentials use local OAuth, CI workload identity, or protected CI credentials. They stay outside application secret documents. Run `pm infra auth --ci` before infrastructure work. Provider adapters own authentication. Mise installs the tools.

If an E2E task needs database or authentication-provider credentials for fixturing, configure those references in fnox. The task can use the same env-file generation path in CI. Ordinary application deployment does not generate local env files or automatically invoke fnox.

GCP fnox access requires Application Default Credentials and permission to read the selected secret. A gcloud CLI login alone does not establish that access. GCP Secret Manager is separate from the GCS backend used for Pulumi state.

## Failures and rotation

Fnox owns secret resolution and temporary JSON files. The project task owns env-file creation. Use private permissions, ignored destinations, and atomic replacement as shown above.

Premise suppresses pull-task output because it can contain secret values. A task or provider failure fails the pull. Do not print credentials or enable shell tracing. Rotate the provider document and pull again to update local files.

## Architecture and references

- [Secrets architecture](architecture/secrets.md)
- [fnox GCP Secret Manager](https://fnox.jdx.dev/providers/gcp-sm.html)
- [Provider authentication](providers/README.md)
