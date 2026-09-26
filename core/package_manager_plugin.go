package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// PackageManagerPlugin parses native package specifications and supplies the
// manager-specific release operations selected by premise.yaml. Core owns
// release sequencing; implementations own package-format behavior.
type PackageManagerPlugin interface {
	ID() string
	// Ecosystem identifies managers that cannot be enabled together. Bun and
	// pnpm both use "npm"; Go and Cargo use distinct ecosystem keys.
	Ecosystem() string
	GetPackageMetadata(root string, template Template) (PackageMetadata, bool, error)
	ValidatePackage(root string, template Template) error
	WillPublishOk(context.Context, string, Template, string, string) (bool, error)
	// CreateGoReleaserConfig returns YAML with builds and/or archives lists,
	// or an empty string when this manager has no downloadable artifacts.
	CreateGoReleaserConfig(root string, manifest Config) (string, error)
	SupportsPackages() bool
	PublishPackages(context.Context, string, string, string, io.Writer, io.Writer) error
	ReleaseWorkspace(context.Context, string, io.Writer, io.Writer) (string, bool, error)
}

var packageManagerRegistry struct {
	sync.RWMutex
	plugins []PackageManagerPlugin
}

// RegisterPackageManagerPlugin registers an implementation by ID. Workspaces
// select registered plugins explicitly through workspace.package_managers.
func RegisterPackageManagerPlugin(plugin PackageManagerPlugin) error {
	if plugin == nil {
		return errors.New("package manager plugin cannot be nil")
	}
	id := plugin.ID()
	if id == "" {
		return errors.New("package manager plugin ID cannot be empty")
	}
	packageManagerRegistry.Lock()
	defer packageManagerRegistry.Unlock()
	for _, registered := range packageManagerRegistry.plugins {
		if registered.ID() == id {
			return fmt.Errorf("package manager plugin %q is already registered", id)
		}
	}
	packageManagerRegistry.plugins = append(packageManagerRegistry.plugins, plugin)
	return nil
}

func packageManagersForConfig(manifest Config) ([]PackageManagerPlugin, error) {
	if len(manifest.Workspace.PackageManagers) == 0 {
		return nil, errors.New("workspace.package_managers must declare at least one package manager")
	}
	packageManagerRegistry.RLock()
	registered := make(map[string]PackageManagerPlugin, len(packageManagerRegistry.plugins))
	for _, plugin := range packageManagerRegistry.plugins {
		registered[plugin.ID()] = plugin
	}
	packageManagerRegistry.RUnlock()

	plugins := make([]PackageManagerPlugin, 0, len(manifest.Workspace.PackageManagers))
	ecosystems := make(map[string]string, len(manifest.Workspace.PackageManagers))
	var failures []error
	for _, id := range manifest.Workspace.PackageManagers {
		plugin, found := registered[id]
		if !found {
			failures = append(failures, fmt.Errorf("package manager plugin %q is not registered", id))
			continue
		}
		ecosystem := plugin.Ecosystem()
		if ecosystem == "" {
			failures = append(failures, fmt.Errorf("package manager plugin %q has no ecosystem", id))
			continue
		}
		if previous, conflict := ecosystems[ecosystem]; conflict {
			failures = append(failures, fmt.Errorf("package managers %q and %q conflict in ecosystem %q", previous, id, ecosystem))
			continue
		}
		ecosystems[ecosystem] = id
		plugins = append(plugins, plugin)
	}
	if err := errors.Join(failures...); err != nil {
		return nil, fmt.Errorf("resolve workspace package managers: %w", err)
	}
	return plugins, nil
}
