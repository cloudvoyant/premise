# GCS infrastructure smoke test

This fixture contains a local template registry and a small Premise client:

```text
registry/templates/bucket/   Go Pulumi program: one private bucket and one object
client/premise.yaml         Dummy workspace with one app
client/apps/dummy/artifact/  The file that pm infra bind supplies to the program
```

`TestInfraGCS` copies the client into a temporary directory, generates the bucket
program from the local registry, and runs real `pm infra` commands. It checks
binding, preview, apply, output, uploaded contents, repeated apply, remote state
from a fresh directory, and destruction. Cleanup runs even when an assertion fails
and confirms that the test bucket returns HTTP 404. It repeats destruction to
check that merge cleanup followed by branch deletion is safe. The existing state bucket is
preserved.

The template uses the published GCP provider. It needs no Neon/Vercel accounts,
application secrets, JavaScript runner, or action-script extraction.

To run locally, authenticate both gcloud and Application Default Credentials:

```sh
gcloud auth login
gcloud auth application-default login
export GCP_PROJECT_ID=your-test-project
export PULUMI_BACKEND_URL=gs://your-existing-state-bucket/premise-e2e
mise run e2e:infra
```

The identity needs permission to create/delete buckets and objects in the test
project and to read/write the state prefix. The bucket is private and lives in
`US`. Every run uses a unique preview stack. `PREMISE_BIN` can select a published
CLI binary; otherwise the test builds the current checkout. An unset
`PULUMI_CONFIG_PASSPHRASE` receives a test-only value for this disposable stack.

The normal `go test ./...` run skips this cloud test. Unit tests in
`core/infra_test.go`, `core/deploy_test.go`, and `core/secrets_test.go` cover stack
selection, validation, target safety, migration/recovery ordering and secret
handling. Deployment tests use fake subprocesses; the bucket smoke test does not
claim to prove a live database migration or application rollback.
