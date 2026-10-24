# Using Premise with Go

## Generate a project

Go projects use the same `apps/<name>` and `libs/<name>` directories as other Premise projects. Each project has its own Mise tools and tasks. All Go projects share the root `go.mod`, dependencies, and repository release version.

```sh
pm generate cloudvoyant/premise-go:premise-cobra-cli
pm generate cloudvoyant/premise-go:premise-connect-grpc-service
pm generate cloudvoyant/premise-go:premise-htmx-app
pm generate cloudvoyant/premise-go:premise-go-lib
pm generate cloudvoyant/premise-go:premise-pulumi-component-lib
```

Enter the workspace Go module path when asked. Use that same path for each project. For example, use `github.com/acme/myrepo` for v0 and v1 releases.

Generation adds the template dependencies to the root manifests. The generated project contains Go packages. It does not need its own `go.mod` or a `go.work` file.

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

## Configure a project

Premise records package settings in the workspace `premise.yaml`:

```yaml
workspace:
  projects:
    - name: math
      path: libs/math
      template: cloudvoyant/premise-go:premise-go-lib
      package:
        publication: public
```

The library template asks whether its API is public or private-local. A public project runs its publication tasks. A private-local project skips those tasks. Both remain in the shared Go module distribution. Use Go `internal/` directories to restrict imports from outside the repository.

Applications can set `package.main` to the directory of their main package, relative to the project. Premise also recognizes a main package in the project root. Set `package.binary` to change the executable name. Libraries do not produce executables unless they declare a main package.

The optional cross-language Pulumi template adds a provider executable and generated SDKs. The native Go Pulumi component template remains a regular Go library. Both have mock tests that run without cloud credentials.

## Use a released library

Every managed Go, Cargo, and Bun package uses the repository release version. A client can fetch one Go package at that version:

```sh
go get github.com/acme/myrepo/libs/math@v1.2.3
```

The client records a dependency on the root Go module. Other Go packages in that module share its version and dependencies.

At v2 and later, Go requires the major version in the module path and imports. For a v4 release, use `github.com/acme/myrepo/v4` in the root `go.mod`:

```sh
go get github.com/acme/myrepo/v4/libs/math@v4.0.0
```

Prepare module paths and imports before a repository major release. This applies even when a change in another language causes the version increase. Premise checks the module path before publication. It does not rewrite imports.

See the [release guide](ci.md) for the shared release flow and the [Go module reference](https://go.dev/ref/mod) for import rules.
