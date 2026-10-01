package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// PackageManagerBackend parses native package specifications and supplies the
// manager-specific release operations selected by premise.yaml. Core owns
// release sequencing; implementations own package-format behavior.
type PackageManagerBackend interface {
	// ID returns the package manager name used in workspace.package_managers.
	ID() string

	// Ecosystem identifies managers that cannot be enabled together. Bun and
	// pnpm both use "npm"; Go and Cargo use distinct ecosystem keys.
	Ecosystem() string

	// GetPackageMetadata reads one template's direct native package, when present.
	GetPackageMetadata(root string, template Template) (PackageMetadata, bool, error)

	// ValidatePackage validates one template's native package metadata.
	ValidatePackage(root string, template Template) error

	// WillPublishOk preflights one package without mutation or publication.
	WillPublishOk(context.Context, string, Template, string, string) (bool, error)

	// PreflightPublication validates workspace-level publication structure and
	// credentials without mutating files or publishing packages.
	PreflightPublication(context.Context, string, string, string) error

	// CreateGoReleaserConfig returns YAML with builds and/or archives lists,
	// or an empty string when this manager has no downloadable artifacts.
	CreateGoReleaserConfig(root string, manifest Config) (string, error)

	// SupportsPackages reports whether the manager publishes registry packages.
	SupportsPackages() bool

	// PublishPackages publishes all eligible packages in the workspace.
	PublishPackages(context.Context, string, string, string, io.Writer, io.Writer) error

	// ReleaseWorkspace prepares and returns the directory used by GoReleaser.
	// The boolean result requests serial artifact builds.
	ReleaseWorkspace(context.Context, string, io.Writer, io.Writer) (string, bool, error)
}

var packageManagerRegistry struct {
	sync.RWMutex
	plugins []PackageManagerBackend
}

// RegisterPackageManagerBackend registers an implementation by ID. Workspaces
// select registered backends explicitly through workspace.package_managers.
func RegisterPackageManagerBackend(backend PackageManagerBackend) error {
	if backend == nil {
		return errors.New("package manager backend cannot be nil")
	}
	id := backend.ID()
	if id == "" {
		return errors.New("package manager backend ID cannot be empty")
	}
	packageManagerRegistry.Lock()
	defer packageManagerRegistry.Unlock()
	for _, registered := range packageManagerRegistry.plugins {
		if registered.ID() == id {
			return fmt.Errorf("package manager backend %q is already registered", id)
		}
	}
	packageManagerRegistry.plugins = append(packageManagerRegistry.plugins, backend)
	return nil
}

func packageManagersForConfig(manifest Config) ([]PackageManagerBackend, error) {
	if len(manifest.Workspace.PackageManagers) == 0 {
		return nil, errors.New("workspace.package_managers must declare at least one package manager")
	}
	packageManagerRegistry.RLock()
	registered := make(map[string]PackageManagerBackend, len(packageManagerRegistry.plugins))
	for _, plugin := range packageManagerRegistry.plugins {
		registered[plugin.ID()] = plugin
	}
	packageManagerRegistry.RUnlock()

	plugins := make([]PackageManagerBackend, 0, len(manifest.Workspace.PackageManagers))
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
