package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PackageManagerPlugin owns package eligibility, artifact selection and registry
// publication. The release coordinator owns version planning, tags and ordering.
// Public visibility is distinct from eligibility: restricted registry packages
// may be published, while a private (non-publishable) package may have artifacts.
type PackageManagerPlugin interface {
	Profile() ReleaseProfile
	Detect(root string) (bool, error)
	IsPublic(root string, template Template) (bool, error)
	ShouldPublishPackage(root string, template Template) (bool, error)
	CreateGoReleaserConfig(root string, manifest Config) (string, error)
	SupportsPackages() bool
	PublishPackages(context.Context, string, string, string, io.Writer, io.Writer) error
	ReleaseWorkspace(context.Context, string, io.Writer, io.Writer) (string, bool, error)
}

type goPackageManager struct{}
type cargoPackageManager struct{}
type bunPackageManager struct{}

var packageManagerPlugins = []PackageManagerPlugin{goPackageManager{}, cargoPackageManager{}, bunPackageManager{}}

func releasePluginForRoot(root string) (PackageManagerPlugin, error) {
	for _, plugin := range packageManagerPlugins {
		found, err := plugin.Detect(root)
		if err != nil {
			return nil, fmt.Errorf("inspect %s release convention: %w", plugin.Profile(), err)
		}
		if found {
			return plugin, nil
		}
	}
	return nil, fmt.Errorf("unsupported release repository: expected go.mod, Cargo.toml, or Bun package.json at %s", root)
}

func releasePluginForProfile(profile ReleaseProfile) (PackageManagerPlugin, error) {
	for _, plugin := range packageManagerPlugins {
		if plugin.Profile() == profile {
			return plugin, nil
		}
	}
	return nil, fmt.Errorf("unsupported release profile %q", profile)
}

func (goPackageManager) Profile() ReleaseProfile    { return ReleaseProfileGo }
func (cargoPackageManager) Profile() ReleaseProfile { return ReleaseProfileCargo }
func (bunPackageManager) Profile() ReleaseProfile   { return ReleaseProfileBun }

func (goPackageManager) Detect(root string) (bool, error) {
	return isRegularFile(filepath.Join(root, "go.mod"))
}
func (cargoPackageManager) Detect(root string) (bool, error) { return isCargoRegistry(root) }
func (bunPackageManager) Detect(root string) (bool, error) {
	packageFile, err := isRegularFile(filepath.Join(root, "package.json"))
	if err != nil || !packageFile {
		return false, err
	}
	return isRegularFile(filepath.Join(root, "bunfig.toml"))
}

func (goPackageManager) IsPublic(_ string, _ Template) (bool, error) { return false, nil }
func (goPackageManager) ShouldPublishPackage(_ string, _ Template) (bool, error) {
	return false, nil
}
func (goPackageManager) SupportsPackages() bool { return false }
func (goPackageManager) PublishPackages(_ context.Context, _, _, _ string, _, _ io.Writer) error {
	return nil
}
func (goPackageManager) ReleaseWorkspace(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
	return root, false, nil
}

func (cargoPackageManager) IsPublic(root string, template Template) (bool, error) {
	pkg, found, err := inspectCargoTemplatePackage(root, template)
	return found && pkg.RegistryPublish, err
}
func (p cargoPackageManager) ShouldPublishPackage(root string, template Template) (bool, error) {
	return p.IsPublic(root, template)
}
func (cargoPackageManager) SupportsPackages() bool { return true }
func (cargoPackageManager) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	if task == "publish" {
		return executeCargoPublish(ctx, root, version, stdout, stderr)
	}
	return publishCargoPackages(ctx, root, version, task, stdout, stderr)
}
func (cargoPackageManager) ReleaseWorkspace(ctx context.Context, root string, stdout, stderr io.Writer) (string, bool, error) {
	workspace, found, err := cargoWorkspaceDirectory(root)
	if err != nil {
		return "", false, fmt.Errorf("inspect Cargo release workspace: %w", err)
	}
	if !found {
		return "", false, errors.New("Cargo release workspace is missing Cargo.toml")
	}
	if err := installReleaseTools(ctx, workspace, stdout, stderr); err != nil {
		return "", false, fmt.Errorf("prepare Cargo release toolchain: %w", err)
	}
	return workspace, true, nil
}

type bunPackage struct {
	Name          string `json:"name"`
	Private       bool   `json:"private"`
	PublishConfig struct {
		Access   string `json:"access"`
		Registry string `json:"registry"`
	} `json:"publishConfig"`
}

func inspectBunTemplatePackage(root string, template Template) (bunPackage, bool, error) {
	directory, err := TemplateDirectory(root, template.Path)
	if err != nil {
		return bunPackage{}, false, err
	}
	path := filepath.Join(directory, "package.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return bunPackage{}, false, nil
	}
	if err != nil {
		return bunPackage{}, false, fmt.Errorf("read Bun package %s: %w", path, err)
	}
	var pkg bunPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return bunPackage{}, false, fmt.Errorf("parse Bun package %s: %w", path, err)
	}
	if pkg.Name == "" {
		return bunPackage{}, false, fmt.Errorf("Bun package %s has no name", path)
	}
	return pkg, true, nil
}

func (bunPackageManager) IsPublic(root string, template Template) (bool, error) {
	pkg, found, err := inspectBunTemplatePackage(root, template)
	return found && !pkg.Private && pkg.PublishConfig.Access == "public", err
}
func (bunPackageManager) ShouldPublishPackage(root string, template Template) (bool, error) {
	pkg, found, err := inspectBunTemplatePackage(root, template)
	return found && !pkg.Private && pkg.PublishConfig.Registry != "", err
}
func (bunPackageManager) SupportsPackages() bool { return true }
func (bunPackageManager) ReleaseWorkspace(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
	return root, false, nil
}
func (bunPackageManager) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	return publishBunPackages(ctx, root, version, task, stdout, stderr)
}

func (goPackageManager) CreateGoReleaserConfig(_ string, manifest Config) (string, error) {
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
`, project, project), nil
}

func (cargoPackageManager) CreateGoReleaserConfig(root string, manifest Config) (string, error) {
	applications := []string{}
	for _, template := range manifest.DeclaredTemplates() {
		if template.Kind != "app" {
			continue
		}
		pkg, found, err := inspectCargoTemplatePackage(root, template)
		if err != nil {
			return "", err
		}
		if found {
			applications = append(applications, pkg.Name)
		}
	}
	if len(applications) == 0 {
		return "", errors.New("cargo release profile requires at least one app template")
	}
	var builder strings.Builder
	builder.WriteString("builds:\n")
	for _, application := range applications {
		fmt.Fprintf(&builder, `  - id: %s
    builder: rust
    binary: %s
    dir: .
    targets:
      - x86_64-unknown-linux-gnu
      - aarch64-unknown-linux-gnu
      - x86_64-apple-darwin
      - aarch64-apple-darwin
    flags:
      - --release
      - -p=%s

`, application, application, application)
	}
	builder.WriteString("archives:\n")
	for _, application := range applications {
		fmt.Fprintf(&builder, `  - id: %s
    ids:
      - %s
    formats:
      - tar.gz
    name_template: "{{ .Binary }}-{{ .Version }}-{{ .Os }}-{{ .Arch }}"

`, application, application)
	}
	return builder.String(), nil
}

// Bun's publishable CLIs are registry packages, not downloadable native
// binaries. Static sites and OCI deployment have no destination configured.
func (bunPackageManager) CreateGoReleaserConfig(_ string, _ Config) (string, error) {
	return "", nil
}
