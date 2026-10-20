# Live S3 infrastructure test

The client owns a small Go Pulumi program that imports the published AWS provider.
It creates a private bucket and one managed object for each preview stack.

```text
client/
├── premise.yaml
├── mise.toml
├── infra/
│   ├── Pulumi.yaml
│   ├── go.mod
│   ├── go.sum
│   └── main.go
└── apps/dummy/artifact/hello.txt
```

The Go harness calls real `pm infra` commands. It checks preview, apply, resource and output queries,
object content through AWS, repeated apply, and saved state from another checkout.
It creates two previews, destroys one, and proves that the other bucket and object still exist.
It destroys both and repeats cleanup. The existing backend bucket remains.

Use a test account and an existing S3 or GCS state backend. The provider creates S3 resources regardless of the state backend. GCS also requires local Google Application Default Credentials.

```sh
aws sso login --profile premise-test
export AWS_PROFILE=premise-test
export AWS_REGION=us-east-1
export PREMISE_TEST_AWS_ACCOUNT=123456789012
export PULUMI_BACKEND_URL=s3://EXISTING_STATE_BUCKET/premise-e2e
# Alternatively: gs://EXISTING_STATE_BUCKET/premise-e2e
mise run e2e:infra
```

The harness checks the actual caller account before resource creation.
The identity needs bucket/object permissions for disposable resources and read/write access to the backend prefix.
`PREMISE_BIN` selects a published CLI binary. Without it, the test builds the checkout.
Normal `go test ./...` skips this test. A skip does not prove live behavior.

This bucket test does not prove migrations or application rollback.
The separate [deployment fixture](../deploy/README.md) checks those behaviors.
