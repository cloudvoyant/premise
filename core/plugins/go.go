package plugins

import (
	"context"
	"fmt"
	"io"

	"github.com/cloudvoyant/premise/core"
)

// Go implements downloadable Go CLI artifacts without registry publication.
type Go struct{}

// ID returns the package manager identifier used by premise.yaml.
func (Go) ID() string { return "go" }

// Ecosystem returns the Go module ecosystem identifier.
func (Go) Ecosystem() string { return "go" }

// GetPackageMetadata reports no registry package metadata for Go artifacts.
func (Go) GetPackageMetadata(_ string, _ core.Template) (core.PackageMetadata, bool, error) {
	return core.PackageMetadata{}, false, nil
}

// ValidatePackage accepts templates because Go has no registry package metadata.
func (Go) ValidatePackage(_ string, _ core.Template) error { return nil }

// WillPublishOk reports that Go does not publish registry packages.
func (Go) WillPublishOk(_ context.Context, _ string, _ core.Template, _, _ string) (bool, error) {
	return false, nil
}

// SupportsPackages reports that Go does not publish registry packages.
func (Go) SupportsPackages() bool { return false }

// PublishPackages is a no-op because Go does not publish registry packages.
func (Go) PublishPackages(_ context.Context, _, _, _ string, _, _ io.Writer) error { return nil }

// ReleaseWorkspace returns the repository root without additional preparation.
func (Go) ReleaseWorkspace(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
	return root, false, nil
}

// CreateGoReleaserConfig returns the Go build and archive definitions.
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
