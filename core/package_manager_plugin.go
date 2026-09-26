package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// PackageManagerPlugin supplies a package manager's detection, publication,
// and artifact policy. Core owns the shared release process; implementations
// can be registered by the CLI or by clients without core importing them.
type PackageManagerPlugin interface {
	ID() string
	Detect(root string) (bool, error)
	IsPublic(root string, template Template) (bool, error)
	ShouldPublishPackage(root string, template Template) (bool, error)
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

// RegisterPackageManagerPlugin adds a plugin in release order. Every matching
// root-level plugin participates; register before releasing. IDs must be unique.
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

// packageManagerForRoot returns a single plugin or a group when multiple root
// conventions match, so the existing release pipeline publishes them together.
func packageManagerForRoot(root string) (PackageManagerPlugin, error) {
	plugins, err := packageManagersForRoot(root)
	if err != nil {
		return nil, err
	}
	if len(plugins) == 1 {
		return plugins[0], nil
	}
	return packageManagerGroup{plugins: plugins}, nil
}

func packageManagersForRoot(root string) ([]PackageManagerPlugin, error) {
	packageManagerRegistry.RLock()
	registered := append([]PackageManagerPlugin(nil), packageManagerRegistry.plugins...)
	packageManagerRegistry.RUnlock()
	var matched []PackageManagerPlugin
	for _, plugin := range registered {
		found, err := plugin.Detect(root)
		if err != nil {
			return nil, fmt.Errorf("inspect %s release convention: %w", plugin.ID(), err)
		}
		if found {
			matched = append(matched, plugin)
		}
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("unsupported release repository at %s: no package manager plugin matched", root)
	}
	return matched, nil
}
