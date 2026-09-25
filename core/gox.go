package core

import "fmt"

// goReleaseBuilds defines the Go CLI archive matrix; the release coordinator
// adds the shared checksum, changelog and GitHub release policy.
func goReleaseBuilds(manifest Config) string {
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
