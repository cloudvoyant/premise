package backends

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core"
	"go.yaml.in/yaml/v3"
)

// Public API -----------------------------------------------------------------

// Go implements Go artifacts and metadata. Module tags are published centrally
// with the repository tag, rather than by a credential-free registry task.
type Go struct{}

// ID returns the package manager identifier used by premise.yaml.
func (Go) ID() string { return "go" }

// Ecosystem returns the Go module ecosystem identifier.
func (Go) Ecosystem() string { return "go" }

// GetPackageMetadata reads a direct Go module and its explicit publication policy.
func (Go) GetPackageMetadata(root string, template core.Template) (core.PackageMetadata, bool, error) {
	dir, err := core.TemplateDirectory(root, template.Path)
	if err != nil {
		return core.PackageMetadata{}, false, err
	}
	m, found, err := core.ReadGoModule(dir)
	if err != nil || !found {
		return core.PackageMetadata{}, found, err
	}
	return core.PackageMetadata{Name: m.Path, Path: dir, PackageManager: "go", Ecosystem: "go", Public: m.Publication == "public", Publishable: m.Publication == "public"}, true, nil
}

// ValidatePackage checks module syntax and release policy.
func (g Go) ValidatePackage(root string, template core.Template) error {
	_, _, err := g.GetPackageMetadata(root, template)
	return err
}

// WillPublishOk validates a module's eligibility without creating tags.
func (g Go) WillPublishOk(_ context.Context, root string, template core.Template, version, task string) (bool, error) {
	if _, err := core.ValidatePackagePublicationVersion(version, task); err != nil {
		return false, err
	}
	m, found, err := g.GetPackageMetadata(root, template)
	return found && m.Publishable, err
}

// PreflightPublication validates standalone consumption before tag publication.
func (Go) PreflightPublication(ctx context.Context, root, version, task string) error {
	if _, err := core.ValidatePackagePublicationVersion(version, task); err != nil {
		return err
	}
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		return err
	}
	return core.ValidateGoModuleRelease(ctx, root, manifest, version)
}

// SupportsPackages reports false because Go has no registry upload step.
func (Go) SupportsPackages() bool { return false }

// PublishPackages is a no-op: module tags are published in the Git release step.
func (Go) PublishPackages(_ context.Context, _, _, _ string, _, _ io.Writer) error { return nil }

// ReleaseWorkspace returns the source repository for native builds.
func (Go) ReleaseWorkspace(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
	return root, false, nil
}

// CreateGoReleaserConfig builds declared Go applications individually. A legacy
// root module without explicit policy keeps Premise's original CLI archive names.
func (Go) CreateGoReleaserConfig(root string, manifest core.Config) (string, error) {
	var builds, archives []map[string]any
	rootModule, found, err := core.ReadGoModule(root)
	if err != nil {
		return "", err
	}
	if found {
		_, policyErr := os.Stat(filepath.Join(root, ".premise-go.toml"))
		if os.IsNotExist(policyErr) {
			var legacy struct {
				Builds   []map[string]any `yaml:"builds"`
				Archives []map[string]any `yaml:"archives"`
			}
			if err := yaml.Unmarshal([]byte(goReleaseBuilds(manifest)), &legacy); err != nil {
				return "", err
			}
			builds = append(builds, legacy.Builds...)
			legacy.Archives[0]["id"] = "go-root"
			legacy.Archives[0]["ids"] = []string{manifest.Workspace.Name}
			archives = append(archives, legacy.Archives...)
		} else if policyErr != nil {
			return "", policyErr
		}
		if policyErr == nil && rootModule.Main != "" {
			builds = append(builds, goProjectBuild(manifest.Workspace.Name, ".", rootModule.Main, rootModule.Binary))
			archives = append(archives, goProjectArchive(manifest.Workspace.Name))
		}
	}
	modules, err := core.GoModules(root, manifest)
	if err != nil {
		return "", err
	}
	for _, m := range modules {
		if m.Main == "" {
			continue
		}
		relative, err := filepath.Rel(root, m.Directory)
		if err != nil {
			return "", err
		}
		name := "go-" + filepath.Base(relative)
		builds = append(builds, goProjectBuild(name, filepath.ToSlash(relative), m.Main, m.Binary))
		archives = append(archives, goProjectArchive(name))
	}
	if len(builds) == 0 {
		return "", nil
	}
	data, err := yaml.Marshal(map[string]any{"builds": builds, "archives": archives})
	return string(data), err
}

// Private helpers ------------------------------------------------------------

func goProjectBuild(name, directory, main, binary string) map[string]any {
	if binary == "" {
		binary = strings.TrimPrefix(name, "go-")
	}
	return map[string]any{"id": name, "dir": directory, "main": main, "binary": binary, "goos": []string{"linux", "darwin"}, "goarch": []string{"amd64", "arm64"}, "env": []string{"CGO_ENABLED=0"}, "flags": []string{"-trimpath"}, "ldflags": []string{"-s -w -X main.version={{ .Version }}"}, "mod_timestamp": "{{ .CommitTimestamp }}"}
}

func goProjectArchive(name string) map[string]any {
	return map[string]any{"id": name, "ids": []string{name}, "formats": []string{"tar.gz"}, "name_template": strings.TrimPrefix(name, "go-") + `-{{ .Tag }}-{{ if eq .Arch "amd64" }}x86_64{{ else }}aarch64{{ end }}-{{ if eq .Os "darwin" }}macos{{ else }}{{ .Os }}{{ end }}`}
}

// goReleaseBuilds preserves the original root Go CLI archive matrix.
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
