# Secret management

Application secrets live in GCP Secret Manager as JSON documents. Fnox retrieves each document and creates a temporary private file. Application tasks read that file through its environment variable.

Premise uses `fnox exec` for local runs and CI. It does not copy application secrets into GitHub or GitLab secrets. Deployment credentials use local OAuth or protected CI credentials and stay outside application secret documents.

## Usage

Pin fnox in the workspace `mise.toml`:

```toml
[tools]
fnox = "1.39.0"
```

Declare an application's configuration and environment profiles in `premise.yaml`:

```yaml
workspace:
  projects:
    - name: web
      template: registry:static-app
      path: apps/web
      secrets:
        config: fnox.toml
        profiles: { dev: dev, stage: stage, prod: prod, preview: preview }
        file: .env.local
```

Create `apps/web/fnox.toml` with references to JSON documents:

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

Each referenced secret contains JSON, such as `{"message":"hello"}`. Store real values in Secret Manager. Commit references only. GCP Secret Manager is separate from the GCS bucket that stores Pulumi state.

Run an application command:

```sh
pm secrets exec --project web --env dev -- mise run start
```

The child receives `APP_CONFIG` as a temporary file path. Its JSON parser reads the values. Fnox removes the file after the child exits, including a failed exit. Premise does not parse the document or maintain a second set of scalar secret keys.

Deployment and migration tasks automatically use fnox when their application declares `secrets`. The selected environment chooses the profile. Recovery tasks run without automatic secret resolution so a secret-provider failure cannot prevent cleanup. If recovery requires secrets, the client task explicitly calls fnox.

### Generate an env file

An env file is a snapshot for tools that require one, including end-to-end tests. Define the project task `secrets:pull`. It writes only dotenv content to standard output. Send diagnostics to standard error and never print credentials there.

```toml
[tasks."secrets:pull"]
run = "python3 tasks/env.py"
```

For example, `tasks/env.py` combines the JSON document with a saved infrastructure output:

```python
import json
import os
import subprocess

with open(os.environ["APP_CONFIG"]) as source:
    secrets = json.load(source)

args = ["pm", "infra", "query", "--env", os.environ["PM_ENV"],
        "--output", "bucketName"]
if os.environ["PM_ENV"] == "preview":
    args += ["--branch", os.environ["PM_BRANCH"]]
bucket = subprocess.check_output(args, text=True).strip()

print("APP_MESSAGE=" + json.dumps(secrets["message"]))
print("BUCKET_NAME=" + json.dumps(bucket))
```

JSON string quoting works for the fixture's dotenv reader. A client with multiline or other special values must use its dotenv library's encoder.

Add the destination to `.gitignore`, then run:

```sh
pm secrets pull --project web --env dev
pm secrets pull --project web --env preview --branch feature/example
```

Premise captures the task output and writes the ignored destination with mode `0600`. It replaces the file only after the task succeeds. The default filename is `.env.local`. Omit `--project` to select all applications with a secrets declaration.

`PM_ENV` and `PM_BRANCH` identify the selected environment. Infra queries inherit `PM_INFRA_BACKEND` when a deployment selects a backend. A profile can use `{branch}` to select the normalized preview identity. That profile must exist in fnox.

## CI authentication

Run native authentication before the environment flow:

```sh
pm infra auth --ci --env-file "$GITHUB_ENV"
pm ci flow on-deploy --environment stage --release none
```

The provider identity needs access to the referenced secrets, infrastructure resources, and state backend. GCP uses Application Default Credentials locally and workload identity federation in CI. A gcloud CLI login alone does not create Application Default Credentials.

Use protected CI credentials only for deployment authentication when OAuth or workload identity is unavailable. Do not store them in the application's JSON document. Do not generate an application env file in CI unless a task requires it.

Fnox and its child inherit the job's credentials. Scope the job identity to the resources it needs. Fnox manages temporary-file lifetime. It does not make arbitrary application output safe to log. Tasks must not print JSON documents, transformed secret values, or shell traces.

## Rotation and failures

Each execution fetches secrets through fnox with daemon caching disabled. Rotate the source document, then run the command again. Regenerate an env snapshot when its values change.

If a profile, document, or task fails, the operation fails. A failed pull leaves the previous env file intact. The pull suppresses task diagnostics because they can contain secret values. Interactive execution forwards child output.

Recovery records contain release identities and artifact references. They do not contain application secrets. Clients own migration compatibility and any recovery task that requires secrets.

## Architecture and references

- [Secrets architecture](architecture/secrets.md)
- [fnox exec](https://fnox.jdx.dev/cli/exec.html)
- [fnox GCP Secret Manager](https://fnox.jdx.dev/providers/gcp-sm.html)
- [Provider authentication](providers/README.md)
