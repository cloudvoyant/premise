package core

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
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
func (bunPackageManager) Detect(root string) (bool, error)   { return isBunRegistry(root) }

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
func (goPackageManager) CreateGoReleaserConfig(_ string, manifest Config) (string, error) {
	return goReleaseBuilds(manifest), nil
}

func (cargoPackageManager) IsPublic(root string, template Template) (bool, error) {
	return cargoTemplateIsPublic(root, template)
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
	return prepareCargoReleaseWorkspace(ctx, root, stdout, stderr)
}
func (cargoPackageManager) CreateGoReleaserConfig(root string, manifest Config) (string, error) {
	return cargoReleaseBuilds(root, manifest)
}

func (bunPackageManager) IsPublic(root string, template Template) (bool, error) {
	return bunTemplateIsPublic(root, template)
}
func (bunPackageManager) ShouldPublishPackage(root string, template Template) (bool, error) {
	return bunTemplateShouldPublish(root, template)
}
func (bunPackageManager) SupportsPackages() bool { return true }
func (bunPackageManager) ReleaseWorkspace(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
	return root, false, nil
}
func (bunPackageManager) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	return publishBunPackages(ctx, root, version, task, stdout, stderr)
}

// Bun's publishable CLIs are registry packages, not downloadable native
// binaries. Static sites and OCI deployment have no destination configured.
func (bunPackageManager) CreateGoReleaserConfig(_ string, _ Config) (string, error) {
	return "", nil
}
