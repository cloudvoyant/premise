# Live GCS infrastructure test

The dummy Premise client owns a small Go Pulumi program. It imports the published GCP provider and creates one private GCS bucket per preview stack. It creates no database, app deployment, or stored object.

```text
client/
├── premise.yaml
├── mise.toml
├── infra/
│   ├── Pulumi.yaml
│   ├── go.mod
│   ├── go.sum
│   └── main.go
└── apps/dummy/mise.toml
```

The Go harness calls real `pm infra` commands: init, plan, apply, query, and destroy.
It creates two preview buckets, reloads saved state from another checkout, and checks repeated apply.
It destroys one preview and checks that the other bucket still exists. Cleanup destroys both, repeats destroy, and checks that both buckets are gone.
The existing backend bucket remains.

Run this test separately on demand. It does not run in CI.
Use a dedicated test project and an existing GCS state backend:

```sh
gcloud auth login
gcloud auth application-default login
export PREMISE_TEST_GCP_PROJECT=YOUR_TEST_PROJECT
export PULUMI_BACKEND_URL=gs://EXISTING_STATE_BUCKET/premise-e2e
mise run e2e:infra
```

The identity needs permission to create and delete buckets in the selected project and read/write access to the backend prefix.
The test passes the explicit project to Pulumi and checks it through gcloud before resource creation.
`PREMISE_BIN` selects a published CLI binary. Without it, the test builds the checkout.
Normal `go test ./...` skips this live test. A skip does not prove live behavior.

The separate local [deployment fixture](../deploy/README.md) checks migrations and application rollback without Neon or Vercel.
