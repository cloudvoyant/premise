# Pulumi workflow fixtures

Both registries are owned by this repository. No external checkout is required.

The infra registry contains a real Go GCS bucket program and resource-mock tests.
The app registry supplies a minimal static HTML artifact and task contracts.
Default E2E tests replace Mise and Pulumi with process doubles.
The resource tests run the GCS program against Pulumi mocks, without cloud access.
The opt-in local-state test uses real Mise/Pulumi for build, stack creation,
configuration inheritance, stable destroy confirmation, and empty preview teardown.
No test creates live GCP resources or validates Neon/Vercel integration.
