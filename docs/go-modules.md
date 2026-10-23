# Go projects and library releases

## Usage

Go applications and libraries use the same `apps/<name>` and `libs/<name>` project layout as other Premise templates. Each project keeps its own Mise tools and tasks. All Go projects share the workspace root `go.mod`, `go.sum`, dependencies, and repository release tag.

```text
premise.yaml
mise.toml
go.mod
go.sum
apps/cli/main.go
apps/cli/mise.toml
libs/math/math.go
libs/math/mise.toml
```

Generate a project and supply the workspace module path when asked:

```sh
pm generate cloudvoyant/premise-go:premise-cobra-cli
pm generate cloudvoyant/premise-go:premise-connect-grpc-service
pm generate cloudvoyant/premise-go:premise-htmx-app
pm generate cloudvoyant/premise-go:premise-go-lib
pm generate cloudvoyant/premise-go:premise-pulumi-component-lib
```

For a new repository, a module path such as `github.com/acme/myrepo` supports v0 and v1 releases. Every subsequent Go project uses that same workspace path. Generation combines the template's dependencies and checksums with the root manifests. It removes the template dependency manifests from the generated project. It does not create a `go.work` or nested module.

For v4, the root module path is `github.com/acme/myrepo/v4`. A client imports `github.com/acme/myrepo/v4/libs/math` and selects the shared repository tag:

```sh
go get github.com/acme/myrepo/v4/libs/math@v4.0.0
```

The client records one dependency on `github.com/acme/myrepo/v4`. All managed Go packages use that version, alongside the Bun and Cargo packages released at the same repository version. Go does not need directory-prefixed tags or a separate sibling dependency preparation command.

## API and build policy

Each Go project can declare `.premise-go.toml`:

```toml
publication = "public"
```

The policy selects the project-owned publication tasks and advertised API packages. `private-local` skips those tasks. Both kinds of packages remain part of the shared module distribution. This flag does not hide source or prevent external imports. Use Go's `internal/` convention when imports need a compiler-enforced boundary. A root `internal/` directory can hold helpers shared by applications and libraries inside the repository.

Applications use `main = "."`. An optional `binary` field selects the executable name. Libraries without a main package produce no executable. A workspace with several Go applications produces separate archives. A workspace containing only Go libraries produces a source-only GitHub release.

## Provider behavior

Go implements the normal package manager interface for native metadata, publication preflight, Mise publication tasks, and artifact configuration. Generic provider capabilities handle native generation files, release tools, and packages distributed through the repository tag. Core release and generation code do not select Go by name. Registry credentials remain isolated from the GitHub publication stage.

Before publication, the Go provider tests public API projects from the committed Git snapshot with `GOWORK=off`. Ignored and untracked files cannot make a broken release pass validation. The provider rejects filesystem replacements in a published root manifest. Stable and RC publication use the existing repository release flow and one root tag.

A shared Go module also shares its minimum Go version. A repository major-version bump requires the matching module path and Go imports, even when another language triggered that bump. Premise validates that path before publication. It does not rewrite imports automatically. Module downloads include eligible files throughout the root module, including non-Go source and assets.

The native Pulumi template is a normal Go library with mock tests. The optional cross-language Pulumi template contains a provider executable and generated Go and Node SDKs. Its schema and SDK regeneration remain project-owned tasks. Neither template requires a cloud deployment for its tests.

The [Go module reference](https://go.dev/ref/mod) describes module versions, import paths, and distribution. Separate Go modules with directory-prefixed tags are outside this implementation and can be added through the provider contract later.
