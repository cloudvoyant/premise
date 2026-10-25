package backends

import (
	"context"
	"fmt"
	"go/version"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/extensions"
	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// Public API -----------------------------------------------------------------

type Go struct{}

func (Go) ID() string                { return "go" }
func (Go) Ecosystem() string         { return "go" }
func (Go) SupportsPackages() bool    { return true }
func (Go) PublishesFromGitTag() bool { return true }

func (Go) GetPackageMetadata(root string, target core.Template) (core.PackageMetadata, bool, error) {
	workspace, found, err := extensions.ReadGoModule(root)
	if err != nil || !found {
		return core.PackageMetadata{}, false, err
	}
	directory, err := core.TemplateDirectory(root, target.Path)
	if err != nil {
		return core.PackageMetadata{}, false, err
	}
	if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
		return core.PackageMetadata{}, false, fmt.Errorf("Go project %s has a nested go.mod; use the workspace root module", target.Name)
	} else if !os.IsNotExist(err) {
		return core.PackageMetadata{}, false, err
	}
	files, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil || len(files) == 0 {
		return core.PackageMetadata{}, false, err
	}
	public := target.Package.IsPublic()
	return core.PackageMetadata{
		Name:           workspace.Path + "/" + filepath.ToSlash(target.Path),
		Path:           directory,
		PackageManager: "go",
		Ecosystem:      "go",
		Public:         public,
		Publishable:    public,
	}, true, nil
}

func (provider Go) ValidatePackage(root string, target core.Template) error {
	_, _, err := provider.GetPackageMetadata(root, target)
	return err
}

func (provider Go) WillPublishOk(ctx context.Context, root string, target core.Template, version, task string) (bool, error) {
	if _, err := core.ValidatePackagePublicationVersion(version, task); err != nil {
		return false, err
	}
	metadata, found, err := provider.GetPackageMetadata(root, target)
	if err != nil || !found || !metadata.Publishable {
		return false, err
	}
	exists, err := (core.MiseTaskRunner{}).TaskExists(ctx, metadata.Path, task)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, fmt.Errorf("Go package %s does not define task %s", metadata.Name, task)
	}
	return true, nil
}

func (provider Go) PreflightPublication(ctx context.Context, root, version, task string) error {
	if _, err := core.ValidatePackagePublicationVersion(version, task); err != nil {
		return err
	}
	if _, err := goPublicationTargets(ctx, root, version, task); err != nil {
		return err
	}
	workspace, found, err := extensions.ReadGoModule(root)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	_, major, ok := module.SplitPathVersion(workspace.Path)
	if !ok {
		return fmt.Errorf("invalid workspace module %s", workspace.Path)
	}
	if err := module.CheckPathMajor("v"+strings.TrimPrefix(version, "v"), major); err != nil {
		return fmt.Errorf("prepare the workspace module path and imports for this major release: %w", err)
	}
	for _, replacement := range workspace.File.Replace {
		if replacement.New.Version == "" {
			return fmt.Errorf("remove filesystem replacement %s before publishing the workspace module", replacement.Old.Path)
		}
	}

	return nil
}

func (provider Go) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	if err := provider.PreflightPublication(ctx, root, version, task); err != nil {
		return err
	}
	targets, err := goPublicationTargets(ctx, root, version, task)
	if err != nil {
		return err
	}
	runner := core.MiseTaskRunner{Stdout: stdout, Stderr: stderr}
	for _, target := range targets {
		if err := runner.Run(ctx, target.Path, []string{"GOWORK=off", "PREMISE_PUBLISH_VERSION=" + strings.TrimPrefix(version, "v")}, "run", task); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s: %s at repository tag %s\n", task, target.Name, version)
	}
	return nil
}

func (Go) ReleaseWorkspace(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
	return root, false, nil
}

func (Go) ReleaseTools(root string, manifest core.Config) ([]string, error) {
	workspace, found, err := extensions.ReadGoModule(root)
	if err != nil || !found {
		return nil, err
	}
	selected := "1.25.0"
	if workspace.File.Go != nil {
		selected = workspace.File.Go.Version
	}
	directories := []string{root}
	for _, target := range manifest.PublicationTargets() {
		directories = append(directories, filepath.Join(root, target.Path))
	}
	for _, directory := range directories {
		data, err := os.ReadFile(filepath.Join(directory, "mise.toml"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		config, err := core.ExtractMiseConfig("package tools", data)
		if err != nil {
			return nil, err
		}
		tool, exists := config.Tools["go"]
		if !exists {
			continue
		}
		candidate := tool.Selector
		if !tool.StringSelector {
			if object, ok := tool.Raw.(map[string]any); ok {
				candidate, _ = object["version"].(string)
			}
		}
		if candidate != "" && version.Compare("go"+candidate, "go"+selected) > 0 {
			selected = candidate
		}
	}
	return []string{"go@" + selected}, nil
}

// ParseDependencies reads module requirements without interpreting release policy.
func (Go) ParseDependencies(manifest []byte) ([]core.Dependency, error) {
	file, err := modfile.Parse("go.mod", manifest, nil)
	if err != nil {
		return nil, err
	}
	dependencies := make([]core.Dependency, 0, len(file.Require))
	for _, requirement := range file.Require {
		dependencies = append(dependencies, core.Dependency{
			Name:     requirement.Mod.Path,
			Scope:    []string{"require"},
			Version:  requirement.Mod.Version,
			Indirect: requirement.Indirect,
		})
	}
	return dependencies, nil
}

// WriteDependencies replaces requirements through Go's native manifest writer.
// Module identity, Go version, replacements, exclusions, and comments stay local.
func (Go) WriteDependencies(manifest []byte, dependencies []core.Dependency) ([]byte, error) {
	file, err := modfile.Parse("go.mod", manifest, nil)
	if err != nil {
		return nil, err
	}
	requirements := make([]*modfile.Require, 0, len(dependencies))
	for _, dependency := range dependencies {
		if len(dependency.Scope) != 1 || dependency.Scope[0] != "require" || dependency.Options != nil {
			return nil, fmt.Errorf("invalid Go requirement %s", dependency.Name)
		}
		if err := module.Check(dependency.Name, dependency.Version); err != nil {
			return nil, err
		}
		requirements = append(requirements, &modfile.Require{
			Mod:      module.Version{Path: dependency.Name, Version: dependency.Version},
			Indirect: dependency.Indirect,
		})
	}
	file.SetRequire(requirements)
	file.SortBlocks()
	file.Cleanup()
	return file.Format()
}

func (Go) ManagedWorkspaceFiles() []string { return []string{"go.mod", "go.sum"} }

func (Go) PrepareGeneration(request core.PackageGenerationContext) ([]core.MergeEntry, error) {
	source, found, err := extensions.ReadGoModule(request.Stage)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	if err := validateGoWorkspaceLayout(request); err != nil {
		return nil, err
	}
	data, err := mergeGoModuleManifests(request, source)
	if err != nil {
		return nil, err
	}
	entries := []core.MergeEntry{request.RootEntry("go.mod", data)}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := os.Remove(filepath.Join(request.Stage, name)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return entries, nil
}

func (Go) CreateGoReleaserConfig(root string, manifest core.Config) (string, error) {
	_, found, err := extensions.ReadGoModule(root)
	if err != nil || !found {
		return "", err
	}
	var builds, archives []map[string]any
	if rootMain, err := extensions.GoDirectoryHasMain(root); err != nil {
		return "", err
	} else if rootMain {
		var legacy struct {
			Builds   []map[string]any `yaml:"builds"`
			Archives []map[string]any `yaml:"archives"`
		}
		if err := yaml.Unmarshal([]byte(goRootReleaseConfig(manifest)), &legacy); err != nil {
			return "", err
		}
		legacy.Archives[0]["id"] = "go-root"
		legacy.Archives[0]["ids"] = []string{manifest.Workspace.Name}
		builds = append(builds, legacy.Builds...)
		archives = append(archives, legacy.Archives...)
	}
	for _, target := range manifest.PublicationTargets() {
		directory := filepath.Join(root, target.Path)
		policy := core.PackageDeclaration{}
		if target.Package != nil {
			policy = *target.Package
		}
		main := policy.Main
		if main == "" {
			main = "."
		}
		hasMain, err := extensions.GoDirectoryHasMain(filepath.Join(directory, main))
		if err != nil {
			return "", err
		}
		if !hasMain {
			continue
		}
		name := "go-" + target.Name
		builds = append(builds, goProjectBuildConfig(name, filepath.ToSlash(target.Path), main, policy.Binary))
		archives = append(archives, goProjectArchiveConfig(name))
	}
	if len(builds) == 0 {
		return "", nil
	}
	data, err := yaml.Marshal(map[string]any{"builds": builds, "archives": archives})
	return string(data), err
}

// Private helpers ------------------------------------------------------------

func goPublicationTargets(ctx context.Context, root, version, task string) ([]core.PackageMetadata, error) {
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		return nil, err
	}
	var targets []core.PackageMetadata
	for _, target := range manifest.PublicationTargets() {
		ready, err := (Go{}).WillPublishOk(ctx, root, target, version, task)
		if err != nil {
			return nil, err
		}
		if !ready {
			continue
		}
		metadata, _, err := (Go{}).GetPackageMetadata(root, target)
		if err != nil {
			return nil, err
		}
		targets = append(targets, metadata)
	}
	return targets, nil
}

func goProjectBuildConfig(name, directory, main, binary string) map[string]any {
	if binary == "" {
		binary = strings.TrimPrefix(name, "go-")
	}
	return map[string]any{
		"id":            name,
		"dir":           directory,
		"main":          main,
		"binary":        binary,
		"goos":          []string{"linux", "darwin"},
		"goarch":        []string{"amd64", "arm64"},
		"env":           []string{"CGO_ENABLED=0"},
		"flags":         []string{"-trimpath"},
		"ldflags":       []string{"-s -w -X main.version={{ .Version }}"},
		"mod_timestamp": "{{ .CommitTimestamp }}",
	}
}

func goProjectArchiveConfig(name string) map[string]any {
	return map[string]any{
		"id":            name,
		"ids":           []string{name},
		"formats":       []string{"tar.gz"},
		"name_template": strings.TrimPrefix(name, "go-") + `-{{ .Tag }}-{{ if eq .Arch "amd64" }}x86_64{{ else }}aarch64{{ end }}-{{ if eq .Os "darwin" }}macos{{ else }}{{ .Os }}{{ end }}`,
	}
}

// goRootReleaseConfig preserves the original root Go CLI archive matrix.
func goRootReleaseConfig(manifest core.Config) string {
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

func validateGoWorkspaceLayout(request core.PackageGenerationContext) error {
	if _, err := os.Lstat(filepath.Join(request.WorkspaceRoot, "go.work")); err == nil {
		return fmt.Errorf("Go generation requires one root module; remove go.work before adding a Go project")
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, path := range request.ProjectPaths {
		if err := request.ValidateLocalPath("", path); err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(request.WorkspaceRoot, path, "go.mod")); err == nil {
			return fmt.Errorf("existing Go project %s has a nested go.mod; migrate it to the root module before generating another Go project", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func mergeGoModuleManifests(request core.PackageGenerationContext, source extensions.GoModule) ([]byte, error) {
	suffix := "/" + filepath.ToSlash(request.ProjectPath)
	if !strings.HasSuffix(source.Path, suffix) {
		return nil, fmt.Errorf("template module %s must end with its project path %s", source.Path, suffix)
	}
	workspacePath := strings.TrimSuffix(source.Path, suffix)
	if err := module.CheckPath(workspacePath); err != nil {
		return nil, err
	}
	original := request.ClientFiles["go.mod"]
	var merged *modfile.File
	if original.Present {
		existing, err := modfile.Parse("go.mod", original.Data, nil)
		if err != nil {
			return nil, err
		}
		if existing.Module == nil {
			return nil, fmt.Errorf("workspace go.mod has no module directive")
		}
		if existing.Module.Mod.Path != workspacePath {
			return nil, fmt.Errorf("workspace Go module is %s; use that module path instead of %s", existing.Module.Mod.Path, workspacePath)
		}
		merged = existing
	} else {
		merged = &modfile.File{}
		if err := merged.AddModuleStmt(workspacePath); err != nil {
			return nil, err
		}
	}
	goVersion := "1.25.0"
	if source.File.Go != nil {
		goVersion = source.File.Go.Version
	}
	if merged.Go == nil || version.Compare("go"+goVersion, "go"+merged.Go.Version) > 0 {
		if err := merged.AddGoStmt(goVersion); err != nil {
			return nil, err
		}
	}
	if shared := request.SharedFiles["go.mod"]; shared.Present {
		contribution, err := modfile.Parse("shared go.mod", shared.Data, nil)
		if err != nil {
			return nil, err
		}
		if contribution.Module == nil || contribution.Module.Mod.Path != workspacePath {
			return nil, fmt.Errorf("shared go.mod must use client repository module %s", workspacePath)
		}
		if len(contribution.Replace) > 0 {
			return nil, fmt.Errorf("shared go.mod must not contain replace directives; configure replacements in the client workspace")
		}
		if contribution.Go != nil && version.Compare("go"+contribution.Go.Version, "go"+merged.Go.Version) > 0 {
			if err := merged.AddGoStmt(contribution.Go.Version); err != nil {
				return nil, err
			}
		}
	}
	if len(source.File.Replace) > 0 {
		return nil, fmt.Errorf("template go.mod must not contain replace directives; configure replacements in the workspace root")
	}
	for _, replacement := range merged.Replace {
		if replacement.New.Version == "" {
			if err := request.ValidateLocalPath("", replacement.New.Path); err != nil {
				return nil, err
			}
		}
	}
	data, err := merged.Format()
	if err != nil {
		return nil, err
	}
	if shared := request.SharedFiles["go.mod"]; shared.Present {
		data, err = core.MergeManifestDependencies(Go{}, "go.mod", shared.Data, data, request.ResolveConflict)
		if err != nil {
			return nil, err
		}
	}
	sourceData, err := source.File.Format()
	if err != nil {
		return nil, err
	}
	return core.MergeManifestDependencies(Go{}, "go.mod", sourceData, data, request.ResolveConflict)
}
