# Go projects and library releases

Each generated Go project has its own module, a set of packages with one dependency list. Premise adds the module to the root `go.work`, which connects local modules during development. The workspace can also contain Bun and Cargo projects.

Generate an application or library from the Go registry:

```sh
pm generate cloudvoyant/premise-go:premise-cobra-cli
pm generate cloudvoyant/premise-go:premise-connect-grpc-service
pm generate cloudvoyant/premise-go:premise-htmx-app
pm generate cloudvoyant/premise-go:premise-go-lib
pm generate cloudvoyant/premise-go:premise-pulumi-component-lib
```

The questionnaire asks for the project name and canonical module path, the import path that identifies the module. For a library at `libs/math`, use a path such as `github.com/acme/myrepo/libs/math`. Public paths must identify the repository directory that contains the module. Libraries also ask whether publication is `public` or `private-local`.

Each project stores its release policy in `.premise-go.toml`:

```toml
publication = "public"
```

Applications use `publication = "private-local"` and `main = "."` by default. The `main` value selects the package that Premise builds as an executable. An optional `binary` value sets the executable name. A library without `main` produces no executable. Missing policy opts out of module publication and preserves existing root CLI builds.

A `private-local` library participates in local workspace builds and receives no publication tag. This policy controls release behavior, not access to source in a public repository. Public libraries cannot depend on unpublished local libraries. Public libraries also cannot contain filesystem `replace` directives.

The repository keeps one shared version for all public libraries. For version `v1.2.3`, Premise creates `v1.2.3` and tags such as `libs/math/v1.2.3` at the same commit. Premise pushes the complete tag set atomically and rejects existing tags that point to another commit. An external client selects one library and its dependencies:

```sh
go get github.com/acme/myrepo/libs/math@v1.2.3
```

If public libraries depend on each other, prepare their requirements before committing the release candidate:

```sh
pm release modules prepare --version v1.2.3
```

Review and commit the resulting `go.mod` and `go.sum` changes before releasing. The command updates existing public sibling requirements to the shared version. It keeps external dependency checksums and excludes speculative checksums for unpublished sibling versions. Release checks run outside `go.work` against temporary copies and a local module proxy, a server layout for module downloads. The release fails if those checks require changes to `go.mod` or if the checkout contains uncommitted files.

For a release candidate, use the shared version such as `v1.2.3-rc.1` during preparation. At version 2 or later, update module paths and imports with the matching suffix, such as `/v2`. Premise fails with migration guidance when those paths do not match the shared version. Premise does not rewrite imports automatically.

The native Pulumi template creates Go components that other Go Pulumi projects consume as a normal module. Its tests use mocks and require no cloud deployment. The separate `premise-pulumi-component-package` template explores a Go provider with generated Go and Node SDKs. That optional template includes provider artifacts and leaves Node package distribution configurable.

The [Go module reference](https://go.dev/ref/mod) describes module paths, workspace behavior, and directory-prefixed tags.
