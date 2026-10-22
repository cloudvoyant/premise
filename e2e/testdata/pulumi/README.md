# Static app on GCS

This is the only live infrastructure fixture. The client owns a Go Pulumi program that creates a private GCS bucket. Its application deploy task uploads a static HTML page into the bucket. No database or app hosting service is involved.

```text
client/
├── premise.yaml
├── mise.toml
├── infra/
│   ├── Pulumi.yaml
│   ├── mise.toml         Local setup task
│   ├── main.go
│   ├── go.mod
│   └── go.sum
├── apps/web/
│   ├── mise.toml
│   ├── fnox.toml         Fake per-environment values
│   └── releases/
│       ├── A/index.html
│       └── B/index.html
└── tasks/static.sh
```

The Go harness runs `pm ci flow on-commit` and `pm ci flow on-deploy` across dev, stage, prod, and a feature-branch preview. A new environment applies infrastructure before its first migration. Existing environments migrate before applying infrastructure.

The fake migration writes the environment and release into `schema-version.txt`. Deployment copies the retained release page to `index.html`. The test verifies the page and marker through GCS.

For rollback, B uploads successfully, then its health task fails. Premise must return a failed CI flow, restore the retained A page, and leave B's migration marker intact. Each environment has its own bucket. Two named remote backends hold the selected environments. The test checks saved state in the selected backend and reuses it from a fresh checkout. Cleanup destroys the disposable buckets and checks deletion. The state bucket remains.

Run separately on demand; this test does not run in CI:

```sh
gcloud auth login
gcloud auth application-default login
export PREMISE_TEST_GCP_PROJECT=YOUR_TEST_PROJECT
export PULUMI_BACKEND_URL=gs://EXISTING_STATE_BUCKET/premise-e2e
mise run e2e:infra
```

The identity needs bucket create/delete permissions in the selected project and read/write access to the backend prefix. Fixture buckets use force-destroy so cleanup also deletes the uploaded test objects.
`PREMISE_BIN` selects a published CLI. Without it, the test builds the checkout.
Normal `go test ./...` skips the live test. A skip does not prove live behavior.

The secrets CLI test uses this same client. It pulls dev, stage, prod, and preview values through real fnox, checks the ignored env file and mode 0600, and verifies that a failed pull preserves the file. The profile values are test literals, not cloud credentials.

```sh
PREMISE_TEST_FNOX=1 go test ./e2e -run '^TestSecretsPull' -count=1
```

Local env-file generation uses an `as_file` JSON secret in each fnox profile. These documents contain test defaults only. The live flow runs `pm secrets pull` after apply. Its Bash `secrets:pull` task writes the document's message and selected stack bucket name into an ignored env file. Preview deployment uses on-commit, manual environments use on-deploy, and preview deletion uses on-merge.
