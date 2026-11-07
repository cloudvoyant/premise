# Deployment failure fixtures

These executables replace Mise and Pulumi in module integration tests. They record calls and inject failures without creating cloud resources.

`core/deploy_test.go` checks that infrastructure and migration failures stop the flow, and that application failures retain the original error when a rollback also fails. The live E2E suite checks real bucket deployment and native rollback; these fixtures cover error paths that it cannot safely trigger.

These files are process doubles, not application tasks. Application task definitions belong in the fixture project’s `mise.toml`.
