package backends

import (
	"context"
	"fmt"
	"go/version"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/extensions"
	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Public API -----------------------------------------------------------------

type Go struct{}

func (Go) ID() string                        { return "go" }
func (Go) Ecosystem() string                 { return "go" }
func (Go) SupportsPackages() bool            { return true }
func (Go) PublishesRepositoryPackages() bool { return true }

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
	public := target.Package != nil && target.Package.Publication == "public"
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
	targets, err := goPublicationTargets(ctx, root, version, task)
	if err != nil {
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
	if len(targets) == 0 {
		return nil
	}
	if err := extensions.RequireCleanRepository(ctx, root); err != nil {
		return err
	}
	snapshot, err := os.MkdirTemp("", "premise-package-snapshot-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(snapshot)
	if err := extensions.ExportRevision(ctx, root, "HEAD", snapshot); err != nil {
		return err
	}
	runner := core.MiseTaskRunner{}
	for _, target := range targets {
		relative, err := filepath.Rel(root, target.Path)
		if err != nil {
			return err
		}
		directory := filepath.Join(snapshot, relative)
		if err := runner.Run(ctx, directory, []string{"GOWORK=off", "MISE_TRUSTED_CONFIG_PATHS=" + snapshot}, "exec", "--", "go", "test", "./..."); err != nil {
			return fmt.Errorf("committed Go package %s fails standalone validation: %w", target.Name, err)
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

func (Go) PrepareGeneration(request core.PackageGenerationContext) ([]core.MergeEntry, error) {
	return prepareGoWorkspace(request)
}
func (Go) PrepareGenerationCandidate(request core.PackageGenerationContext, candidate string) error {
	return copyGoWorkspaceSources(request.WorkspaceRoot, candidate)
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
		if err := yaml.Unmarshal([]byte(goReleaseBuilds(manifest)), &legacy); err != nil {
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
		builds = append(builds, goProjectBuild(name, filepath.ToSlash(target.Path), main, policy.Binary))
		archives = append(archives, goProjectArchive(name))
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

func goProjectBuild(name, directory, main, binary string) map[string]any {
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

func goProjectArchive(name string) map[string]any {
	return map[string]any{
		"id":            name,
		"ids":           []string{name},
		"formats":       []string{"tar.gz"},
		"name_template": strings.TrimPrefix(name, "go-") + `-{{ .Tag }}-{{ if eq .Arch "amd64" }}x86_64{{ else }}aarch64{{ end }}-{{ if eq .Os "darwin" }}macos{{ else }}{{ .Os }}{{ end }}`,
	}
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

// prepareGoWorkspace promotes a template's dependencies into the shared root
// manifest. Generated projects contain packages, not nested Go modules.
func prepareGoWorkspace(request core.PackageGenerationContext) ([]core.MergeEntry, error) {
	source, found, err := extensions.ReadGoModule(request.Stage)
	if err != nil || !found {
		return nil, err
	}
	suffix := "/" + filepath.ToSlash(request.ProjectPath)
	if !strings.HasSuffix(source.Path, suffix) {
		return nil, fmt.Errorf("template module %s must end with its project path %s", source.Path, suffix)
	}
	workspacePath := strings.TrimSuffix(source.Path, suffix)
	if err := module.CheckPath(workspacePath); err != nil {
		return nil, err
	}
	existing, exists, err := extensions.ReadGoModule(request.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	var merged *modfile.File
	if exists {
		if existing.Path != workspacePath {
			return nil, fmt.Errorf("workspace Go module is %s; use that module path instead of %s", existing.Path, workspacePath)
		}
		merged = existing.File
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
	requirements := map[string]*modfile.Require{}
	for _, requirement := range merged.Require {
		copy := *requirement
		requirements[copy.Mod.Path] = &copy
	}
	for _, requirement := range source.File.Require {
		current, exists := requirements[requirement.Mod.Path]
		if !exists {
			copy := *requirement
			requirements[copy.Mod.Path] = &copy
			continue
		}
		if semver.Compare(requirement.Mod.Version, current.Mod.Version) > 0 {
			current.Mod.Version = requirement.Mod.Version
		}
		current.Indirect = current.Indirect && requirement.Indirect
	}
	combined := make([]*modfile.Require, 0, len(requirements))
	for _, requirement := range requirements {
		combined = append(combined, requirement)
	}
	merged.SetRequire(combined)
	if len(source.File.Replace) > 0 {
		return nil, fmt.Errorf("template go.mod must not contain replace directives; configure replacements in the workspace root")
	}
	merged.SortBlocks()
	merged.Cleanup()
	data, err := merged.Format()
	if err != nil {
		return nil, err
	}
	entry, err := goWorkspaceEntry(request.WorkspaceRoot, "go.mod", data)
	if err != nil {
		return nil, err
	}
	entries := []core.MergeEntry{entry}
	sums := map[string]bool{}
	for _, directory := range []string{request.WorkspaceRoot, request.Stage} {
		contents, err := os.ReadFile(filepath.Join(directory, "go.sum"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(contents), "\n") {
			if line != "" {
				sums[line] = true
			}
		}
	}
	if len(sums) > 0 {
		lines := make([]string, 0, len(sums))
		for line := range sums {
			lines = append(lines, line)
		}
		sort.Strings(lines)
		entry, err := goWorkspaceEntry(request.WorkspaceRoot, "go.sum", []byte(strings.Join(lines, "\n")+"\n"))
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := os.Remove(filepath.Join(request.Stage, name)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return entries, nil
}

// copyGoWorkspaceSources supplies existing packages and assets to isolated
// generation checks without copying dependency caches or build output.
func copyGoWorkspaceSources(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if relative == "bin" || relative == "dist" || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(destination, relative), 0755)
		}
		// Root files are already staged by generation.
		if !strings.ContainsRune(relative, filepath.Separator) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, relative), contents, info.Mode().Perm())
	})
}

func goWorkspaceEntry(root, name string, data []byte) (core.MergeEntry, error) {
	selected := core.PathValue{Kind: "file", Mode: 0644}
	previous, err := os.ReadFile(filepath.Join(root, name))
	if err == nil {
		selected.Present = true
		selected.Data = previous
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			return core.MergeEntry{}, err
		}
		selected.Mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return core.MergeEntry{}, err
	}
	return core.MergeEntry{Path: name, Selected: selected, Output: core.PathValue{Present: true, Kind: "file", Mode: selected.Mode, Data: data}, Strategy: core.MergeStrategyCopy}, nil
}
