package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gobwas/glob"
)

// Public API -----------------------------------------------------------------

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

	// SupportsPackages reports whether the manager handles language packages.
	SupportsPackages() bool

	// PublishPackages publishes all eligible packages in the workspace.
	PublishPackages(context.Context, string, string, string, io.Writer, io.Writer) error

	// ReleaseWorkspace prepares and returns the directory used by GoReleaser.
	// The boolean result requests serial artifact builds.
	ReleaseWorkspace(context.Context, string, io.Writer, io.Writer) (string, bool, error)
}

// PackageGenerationContext identifies the selected template and its destination.
// ClientFiles contains original client values; SharedFiles contains declared registry
// contributions. Providers return native contents; core owns file snapshots and modes.
type PackageGenerationContext struct {
	WorkspaceRoot   string
	WorkspaceName   string
	ProjectPaths    []string
	ProjectPath     string
	Stage           string
	ClientFiles     map[string]PathValue
	SharedFiles     map[string]PathValue
	ResolveConflict MergeConflictResolver
}

// PackageManagerGeneration prepares native workspace files when a template is
// generated. Providers without native workspace preparation need no hook.
type PackageManagerGeneration interface {
	// GenerationRootFiles declares exclusive native roots, including lock outputs.
	GenerationRootFiles() []string
	PrepareGeneration(PackageGenerationContext) ([]MergeEntry, error)
}

// PackageManagerReleaseTools supplies tools needed to build native artifacts.
type PackageManagerReleaseTools interface {
	ReleaseTools(string, Config) ([]string, error)
}

// PackageManagerRepositoryPublication identifies packages distributed by the
// shared repository tag. Registry publishers keep their existing upload stage.
type PackageManagerRepositoryPublication interface {
	PublishesRepositoryPackages() bool
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

// RootEntry supplies native contents with the original client mode when present.
func (request PackageGenerationContext) RootEntry(path string, data []byte) MergeEntry {
	mode := request.ClientFiles[path].Mode
	if mode == 0 {
		mode = 0644
	}
	return MergeEntry{Path: path, Output: PathValue{Present: true, Kind: "file", Mode: mode, Data: data}, Strategy: MergeStrategyCopy}
}

// MatchesProject checks a native member pattern against the selected project.
func (request PackageGenerationContext) MatchesProject(pattern string) (bool, error) {
	matcher, err := glob.Compile(strings.TrimPrefix(pattern, "./"), '/')
	if err != nil {
		return false, fmt.Errorf("invalid workspace pattern %q: %w", pattern, err)
	}
	return matcher.Match(filepath.ToSlash(request.ProjectPath)), nil
}

// ValidateLocalPath rejects references that could resolve outside the disposable workspace.
func (request PackageGenerationContext) ValidateLocalPath(base, path string) error {
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute local dependency path %q is unsupported; use a workspace-relative path so candidate validation cannot read the live workspace", path)
	}
	absolute := filepath.Join(request.WorkspaceRoot, base, filepath.FromSlash(path))
	relative, err := filepath.Rel(request.WorkspaceRoot, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("local dependency path %q escapes workspace; generation supports local sources within the workspace root", path)
	}
	return nil
}

// Private helpers ------------------------------------------------------------

var packageManagerRegistry struct {
	sync.RWMutex
	plugins []PackageManagerBackend
}

func packageManagersForConfig(manifest Config) ([]PackageManagerBackend, error) {
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
