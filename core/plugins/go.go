package plugins

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/cloudvoyant/premise/core"
)

type Go struct{}

func (Go) ID() string                                                              { return "go" }
func (Go) Detect(root string) (bool, error)                                        { return core.IsRegularFile(filepath.Join(root, "go.mod")) }
func (Go) IsPublic(_ string, _ core.Template) (bool, error)                        { return false, nil }
func (Go) ShouldPublishPackage(_ string, _ core.Template) (bool, error)            { return false, nil }
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
