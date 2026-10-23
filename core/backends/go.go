package backends

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"go/version"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/extensions"
	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/module"
)

// Public API -----------------------------------------------------------------

type Go struct{}

func (Go) ID() string                        { return "go" }
func (Go) Ecosystem() string                 { return "go" }
func (Go) SupportsPackages() bool            { return true }
func (Go) PublishesRepositoryPackages() bool { return true }

func (Go) GetPackageMetadata(root string, target core.Template) (core.PackageMetadata, bool, error) {
	workspace, found, err := readGoModule(root)
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
	policy, handled, err := readGoProjectPolicy(directory)
	if err != nil || !handled {
		return core.PackageMetadata{}, false, err
	}
	return core.PackageMetadata{
		Name: workspace.Path + "/" + filepath.ToSlash(target.Path), Path: directory,
		PackageManager: "go", Ecosystem: "go", Public: policy.Publication == "public", Publishable: policy.Publication == "public",
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
	workspace, found, err := readGoModule(root)
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
	workspace, found, err := readGoModule(root)
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
	_, found, err := readGoModule(root)
	if err != nil || !found {
		return "", err
	}
	var builds, archives []map[string]any
	if rootMain, err := goDirectoryHasMain(root); err != nil {
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
		policy, handled, err := readGoProjectPolicy(directory)
		if err != nil {
			return "", err
		}
		if !handled {
			continue
		}
		main := policy.Main
		if main == "" {
			hasMain, err := goDirectoryHasMain(directory)
			if err != nil {
				return "", err
			}
			if hasMain {
				main = "."
			}
		}
		if main == "" {
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

type goProjectPolicy struct {
	Publication string `toml:"publication"`
	Main        string `toml:"main"`
	Binary      string `toml:"binary"`
}

func readGoProjectPolicy(directory string) (goProjectPolicy, bool, error) {
	policy := goProjectPolicy{Publication: "private-local"}
	data, err := os.ReadFile(filepath.Join(directory, ".premise-go.toml"))
	if os.IsNotExist(err) {
		files, err := filepath.Glob(filepath.Join(directory, "*.go"))
		return policy, len(files) > 0, err
	}
	if err != nil {
		return policy, false, err
	}
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&policy); err != nil {
		return policy, false, err
	}
	if policy.Publication != "public" && policy.Publication != "private-local" {
		return policy, false, fmt.Errorf("invalid Go API policy %q", policy.Publication)
	}
	if policy.Main != "" && policy.Main != "." && !filepath.IsLocal(policy.Main) {
		return policy, false, fmt.Errorf("Go main must be inside its project")
	}
	if policy.Binary != "" {
		if err := core.ValidateProjectName(policy.Binary); err != nil {
			return policy, false, err
		}
	}
	return policy, true, nil
}

func goDirectoryHasMain(directory string) (bool, error) {
	files, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		return false, err
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.PackageClauseOnly)
		if err != nil {
			return false, err
		}
		if parsed.Name.Name == "main" {
			return true, nil
		}
	}
	return false, nil
}

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
