package plugins

import (
	"context"
	"fmt"
	"io"

	"github.com/cloudvoyant/premise/core"
)

type Go struct{}

func (Go) ID() string        { return "go" }
func (Go) Ecosystem() string { return "go" }
func (Go) GetPackageMetadata(_ string, _ core.Template) (core.PackageMetadata, bool, error) {
	return core.PackageMetadata{}, false, nil
}
func (Go) ValidatePackage(_ string, _ core.Template) error { return nil }
func (Go) WillPublishOk(_ context.Context, _ string, _ core.Template, _, _ string) (bool, error) {
	return false, nil
}
func (Go) SupportsPackages() bool                                                  { return false }
func (Go) PublishPackages(_ context.Context, _, _, _ string, _, _ io.Writer) error { return nil }
func (Go) ReleaseWorkspace(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
	return root, false, nil
}
func (Go) CreateGoReleaserConfig(_ string, manifest core.Config) (string, error) {
	return goReleaseBuilds(manifest), nil
}

// goReleaseBuilds defines the Go CLI archive matrix; core adds the shared
// checksum, changelog and GitHub release policy.
func goReleaseBuilds(manifest core.Config) string {
	project := manifest.Workspace.Name
	return fmt.Sprintf(`builds:
  - id: %s
    main: .
    binary: %s
    goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
    mod_timestamp: "{{ .CommitTimestamp }}"

archives:
  - formats:
      - tar.gz
    name_template: >-
      {{ .ProjectName }}-{{ .Tag }}-
      {{- if eq .Arch "amd64" }}x86_64{{- else }}aarch64{{- end }}-
      {{- if eq .Os "darwin" }}macos{{- else }}{{ .Os }}{{- end }}
`, project, project)
}
