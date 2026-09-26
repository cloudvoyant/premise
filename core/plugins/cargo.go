package plugins

import (
	"context"
	"io"

	"github.com/cloudvoyant/premise/core"
)

type Cargo struct{}

func (Cargo) ID() string                       { return "cargo" }
func (Cargo) Detect(root string) (bool, error) { return isCargoRegistry(root) }
func (Cargo) IsPublic(root string, template core.Template) (bool, error) {
	return cargoTemplateIsPublic(root, template)
}
func (p Cargo) ShouldPublishPackage(root string, template core.Template) (bool, error) {
	return p.IsPublic(root, template)
}
func (Cargo) SupportsPackages() bool { return true }
func (Cargo) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	return publishCargoPackages(ctx, root, version, task, stdout, stderr)
}
func (Cargo) ReleaseWorkspace(ctx context.Context, root string, stdout, stderr io.Writer) (string, bool, error) {
	return prepareCargoReleaseWorkspace(ctx, root, stdout, stderr)
}
func (Cargo) CreateGoReleaserConfig(root string, manifest core.Config) (string, error) {
	return cargoReleaseBuilds(root, manifest)
}
