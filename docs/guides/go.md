# Using Premise with Go

## Generate a project

Go applications use `apps/<name>` and libraries use `libs/<name>`. All Go projects share the repository's root module, dependencies, and release version.

Choose a template:

```sh
pm generate cloudvoyant/premise-go:premise-cobra-cli
pm generate cloudvoyant/premise-go:premise-connect-grpc-service
pm generate cloudvoyant/premise-go:premise-htmx-app
pm generate cloudvoyant/premise-go:premise-go-lib
pm generate cloudvoyant/premise-go:premise-pulumi-component-lib
```

When asked for the workspace Go module path, enter the repository path, such as `github.com/acme/myrepo`. Use the same path for every application and library. Do not append `apps/cli` or `libs/math` to this answer.

Premise creates the root `go.mod` when you generate the first Go project. It adds `go.sum` when dependencies need checksums. Later projects update these same files. The root module declaration uses the repository path:

```go
module github.com/acme/myrepo
```

Your workspace then contains:

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

## Choose publication tasks

The library template asks whether to run publication tasks. Premise records your answer in `premise.yaml`:

```yaml
workspace:
  projects:
    - name: math
      path: libs/math
      template: cloudvoyant/premise-go:premise-go-lib
      package:
        public: true
```

Set `public: false` to skip that project's publication tasks. If you omit the field, it defaults to `true`. Both settings keep the source in the shared module download. To restrict external imports, use Go's [internal directories](https://pkg.go.dev/cmd/go#hdr-Internal_Directories).

## Use a released library

Every managed package uses the repository release version. A client can fetch an individual Go library at that version:

```sh
go get github.com/acme/myrepo/libs/math@v1.2.3
```

The client depends on the root Go module. Its packages share a version and dependency set, as described in the [Go module reference](https://go.dev/ref/mod#modules-packages-versions).

## Prepare a new major version

Go requires a major-version suffix from v2 onward. For example, a v2 module uses `github.com/acme/myrepo/v2`. See [Go's major-version rules](https://go.dev/ref/mod#major-version-suffixes).

Before the next repository major release:

1. From the workspace root, update the module path:

   ```sh
   go mod edit -module=github.com/acme/myrepo/v2
   ```

2. Update imports of your own packages throughout the workspace. For example:

   ```go
   // Before
   import "github.com/acme/myrepo/libs/math"

   // After
   import "github.com/acme/myrepo/v2/libs/math"
   ```

3. If a project generates Go code, update its generation configuration and run its `generate` task.
4. Run `go mod tidy` from the root, then run the project tests.
5. Commit the module, import, and generated-file changes before the release. Continue with the [normal release flow](ci.md).

For each later major version, replace the suffix with that version, such as `/v3`. These steps also apply when a change in another language causes the repository major release. Premise checks the Go module path but does not update imports for you.

After the v2 release, clients update their imports and fetch the new version:

```sh
go get github.com/acme/myrepo/v2/libs/math@v2.0.0
```

## References

- [Go modules, packages, and versions](https://go.dev/ref/mod#modules-packages-versions): module and package paths.
- [Go major-version suffixes](https://go.dev/ref/mod#major-version-suffixes): path requirements from v2 onward.
- [Developing a major version update](https://go.dev/doc/modules/major-version): module and import changes for maintainers and clients.
- [Go internal directories](https://pkg.go.dev/cmd/go#hdr-Internal_Directories): compiler-enforced import restrictions.
