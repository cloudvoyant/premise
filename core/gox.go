package core

import (
	"bytes"
	"context"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/mod/zip"
)

// Public API -----------------------------------------------------------------

// GoModule describes a project's native Go module and explicit release policy.
// Missing policy opts out of publication and preserves legacy root CLI builds.
type GoModule struct {
	Path        string        `toml:"-"`
	Directory   string        `toml:"-"`
	Publication string        `toml:"publication"`
	Main        string        `toml:"main"`
	Binary      string        `toml:"binary"`
	File        *modfile.File `toml:"-"`
}

// ReadGoModule parses go.mod and optional .premise-go.toml in one directory.
func ReadGoModule(directory string) (GoModule, bool, error) {
	data, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if os.IsNotExist(err) {
		return GoModule{}, false, nil
	}
	if err != nil {
		return GoModule{}, false, err
	}
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return GoModule{}, false, err
	}
	if file.Module == nil {
		return GoModule{}, false, fmt.Errorf("%s/go.mod has no module directive", directory)
	}
	if err := module.CheckPath(file.Module.Mod.Path); err != nil {
		return GoModule{}, false, err
	}
	result := GoModule{Path: file.Module.Mod.Path, Directory: directory, Publication: "private-local", File: file}
	policy, err := os.ReadFile(filepath.Join(directory, ".premise-go.toml"))
	if err != nil && !os.IsNotExist(err) {
		return GoModule{}, false, err
	}
	if err == nil {
		if err := toml.NewDecoder(bytes.NewReader(policy)).DisallowUnknownFields().Decode(&result); err != nil {
			return GoModule{}, false, fmt.Errorf("Go release policy: %w", err)
		}
	}
	if result.Publication != "public" && result.Publication != "private-local" {
		return GoModule{}, false, fmt.Errorf("invalid Go publication %q: expected public or private-local", result.Publication)
	}
	if result.Main != "" && result.Main != "." && !filepath.IsLocal(result.Main) {
		return GoModule{}, false, fmt.Errorf("Go main %q must be inside its module", result.Main)
	}
	if result.Binary != "" {
		if result.Binary == "." || result.Binary == ".." {
			return GoModule{}, false, fmt.Errorf("invalid Go binary name %q", result.Binary)
		}
		if err := ValidateProjectName(result.Binary); err != nil {
			return GoModule{}, false, fmt.Errorf("Go binary: %w", err)
		}
	}
	return result, true, nil
}

// GoModules lists direct Go modules for the selected workspace lifecycle.
func GoModules(root string, manifest Config) ([]GoModule, error) {
	var result []GoModule
	// Registry templates contain questionnaire tokens and are scaffold sources,
	// not external modules released from this repository.
	if manifest.Workspace.Kind == ProjectKindTemplateRegistry {
		return result, nil
	}
	seen := map[string]bool{}
	for _, target := range manifest.PublicationTargets() {
		directory, err := TemplateDirectory(root, target.Path)
		if err != nil {
			return nil, err
		}
		m, found, err := ReadGoModule(directory)
		if err != nil {
			return nil, fmt.Errorf("project %s: %w", target.Name, err)
		}
		if !found {
			continue
		}
		if seen[m.Path] {
			return nil, fmt.Errorf("duplicate Go module %s", m.Path)
		}
		seen[m.Path] = true
		result = append(result, m)
	}
	return result, nil
}

// GoModuleTags validates publication policy and returns prefixed module tags.
// It never mutates the release checkout or advances independent module versions.
func GoModuleTags(root string, manifest Config, version string) ([]string, error) {
	if !releaseVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("invalid Go release version %q", version)
	}
	modules, err := GoModules(root, manifest)
	if err != nil {
		return nil, err
	}
	byPath := map[string]GoModule{}
	for _, m := range modules {
		byPath[m.Path] = m
	}
	var tags []string
	for _, m := range modules {
		if m.Publication != "public" {
			continue
		}
		_, major, ok := module.SplitPathVersion(m.Path)
		if !ok {
			return nil, fmt.Errorf("invalid Go module path %s", m.Path)
		}
		if err := module.CheckPathMajor(version, major); err != nil {
			return nil, fmt.Errorf("module %s: prepare its major-version path and imports before release: %w", m.Path, err)
		}
		for _, replace := range m.File.Replace {
			if replace.New.Version == "" {
				return nil, fmt.Errorf("public Go module %s has local replace %s; remove it before releasing", m.Path, replace.Old.Path)
			}
		}
		for _, require := range m.File.Require {
			sibling, local := byPath[require.Mod.Path]
			if !local {
				continue
			}
			if sibling.Publication != "public" {
				return nil, fmt.Errorf("public Go module %s depends on unpublished local module %s", m.Path, sibling.Path)
			}
			if require.Mod.Version != version {
				return nil, fmt.Errorf("module %s requires sibling %s at %s; run pm release modules prepare --version %s and commit the result before releasing", m.Path, sibling.Path, require.Mod.Version, version)
			}
		}
		relative, err := filepath.Rel(root, m.Directory)
		if err != nil || !filepath.IsLocal(relative) {
			return nil, fmt.Errorf("module %s escapes release root", m.Path)
		}
		// For the standard repository paths, the directory must match the module suffix.
		base, _, _ := module.SplitPathVersion(m.Path)
		if !strings.HasSuffix(base, "/"+filepath.ToSlash(relative)) {
			return nil, fmt.Errorf("public module %s must end with its repository directory /%s", m.Path, filepath.ToSlash(relative))
		}
		tags = append(tags, filepath.ToSlash(relative)+"/"+version)
	}
	return tags, nil
}

// ValidateGoModuleRelease tests public modules with workspace resolution disabled
// against a temporary module proxy containing all coordinated public siblings.
func ValidateGoModuleRelease(ctx context.Context, root string, manifest Config, version string) error {
	tags, err := GoModuleTags(root, manifest, version)
	if err != nil || len(tags) == 0 {
		return err
	}
	modules, err := GoModules(root, manifest)
	if err != nil {
		return err
	}
	proxy, err := os.MkdirTemp("", "premise-go-proxy-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(proxy)
	if err := writeGoModuleProxy(proxy, modules, version); err != nil {
		return err
	}
	for _, m := range modules {
		if m.Publication != "public" {
			continue
		}
		candidate, err := os.MkdirTemp("", "premise-go-release-*")
		if err != nil {
			return err
		}
		err = func() error {
			defer os.RemoveAll(candidate)
			if err := copyTree(m.Directory, candidate, nil); err != nil {
				return err
			}
			before, err := os.ReadFile(filepath.Join(candidate, "go.mod"))
			if err != nil {
				return err
			}
			command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "./...")
			command.Dir = candidate
			command.Env = goProxyEnvironment(proxy)
			data, err := command.CombinedOutput()
			if err != nil {
				return fmt.Errorf("standalone public module %s failed: %w\n%s", m.Path, err, data)
			}
			after, err := os.ReadFile(filepath.Join(candidate, "go.mod"))
			if err != nil {
				return err
			}
			if !bytes.Equal(before, after) {
				return fmt.Errorf("public module %s requires go.mod changes; tidy and commit it before release", m.Path)
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// PrepareGoModuleDependencies updates coordinated public sibling requirements
// before the user commits the release candidate. It never commits or tags.
func PrepareGoModuleDependencies(ctx context.Context, root, version string) error {
	if !semver.IsValid(version) || !releaseVersionPattern.MatchString(version) {
		return fmt.Errorf("invalid Go release version %q", version)
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	modules, err := GoModules(root, manifest)
	if err != nil {
		return err
	}
	publicPaths := make(map[string]bool, len(modules))
	for _, projectModule := range modules {
		publicPaths[projectModule.Path] = projectModule.Publication == "public"
	}

	stagingRoot, err := os.MkdirTemp("", "premise-go-prepare-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingRoot)
	staged, err := stagePublicGoModules(modules, publicPaths, version, stagingRoot)
	if err != nil {
		return err
	}

	proxyModules := make([]GoModule, 0, len(staged))
	for _, projectModule := range staged {
		proxyModules = append(proxyModules, projectModule.prepared)
	}
	proxy := filepath.Join(stagingRoot, "proxy")
	if err := writeGoModuleProxy(proxy, proxyModules, version); err != nil {
		return err
	}
	if err := tidyStagedGoModules(ctx, staged, proxy); err != nil {
		return err
	}
	entries, err := stagedGoModuleEntries(root, staged, publicPaths, version)
	if err != nil {
		return err
	}

	// Source files change only here, after every staged module succeeds.
	transaction := &GeneratePlan{workspaceRoot: root, Entries: entries}
	return transaction.publishRoot()
}

// Private helpers ------------------------------------------------------------

// stagedGoModule keeps the source location separate from its editable copy.
type stagedGoModule struct {
	original GoModule
	prepared GoModule
}

// stagePublicGoModules edits sibling requirements only in temporary copies.
func stagePublicGoModules(modules []GoModule, publicPaths map[string]bool, version, stagingRoot string) ([]stagedGoModule, error) {
	var staged []stagedGoModule
	for index, original := range modules {
		if original.Publication != "public" {
			continue
		}
		directory := filepath.Join(stagingRoot, fmt.Sprint(index))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, err
		}
		if err := copyTree(original.Directory, directory, nil); err != nil {
			return nil, err
		}
		prepared, found, err := ReadGoModule(directory)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("staged module %s has no go.mod", original.Path)
		}
		for _, requirement := range prepared.File.Require {
			publishable, local := publicPaths[requirement.Mod.Path]
			if !local {
				continue
			}
			if !publishable {
				return nil, fmt.Errorf("public module %s depends on unpublished module %s", prepared.Path, requirement.Mod.Path)
			}
			if err := prepared.File.AddRequire(requirement.Mod.Path, version); err != nil {
				return nil, err
			}
		}
		data, err := prepared.File.Format()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(prepared.Directory, "go.mod"), data, 0o644); err != nil {
			return nil, err
		}
		staged = append(staged, stagedGoModule{original: original, prepared: prepared})
	}
	return staged, nil
}

func tidyStagedGoModules(ctx context.Context, staged []stagedGoModule, proxy string) error {
	for _, projectModule := range staged {
		command := exec.CommandContext(ctx, "go", "mod", "tidy")
		command.Dir = projectModule.prepared.Directory
		command.Env = goProxyEnvironment(proxy)
		data, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("prepare module %s: %w\n%s", projectModule.prepared.Path, err, data)
		}
	}
	return nil
}

// stagedGoModuleEntries collects all outputs without changing source files.
func stagedGoModuleEntries(root string, staged []stagedGoModule, publicPaths map[string]bool, version string) ([]MergeEntry, error) {
	var entries []MergeEntry
	for _, projectModule := range staged {
		for _, name := range []string{"go.mod", "go.sum"} {
			data, err := os.ReadFile(filepath.Join(projectModule.prepared.Directory, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if name == "go.sum" {
				data = removeCandidateGoChecksums(data, publicPaths, version)
			}
			entry, err := preparedGoModuleEntry(root, projectModule.original.Directory, name, data)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// Candidate archives change during preparation, so their checksums cannot be
// committed as checksums of the final published modules. Keep external sums.
func removeCandidateGoChecksums(data []byte, publicPaths map[string]bool, version string) []byte {
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && publicPaths[fields[0]] && strings.TrimSuffix(fields[1], "/go.mod") == version {
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func preparedGoModuleEntry(root, directory, name string, data []byte) (MergeEntry, error) {
	path := filepath.Join(directory, name)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return MergeEntry{}, err
	}
	selected := PathValue{Kind: "file", Mode: 0o644}
	previous, err := os.ReadFile(path)
	if err == nil {
		selected.Present = true
		selected.Data = previous
		info, err := os.Stat(path)
		if err != nil {
			return MergeEntry{}, err
		}
		selected.Mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return MergeEntry{}, err
	}
	return MergeEntry{
		Path:     filepath.ToSlash(relative),
		Selected: selected,
		Output:   PathValue{Present: true, Kind: "file", Mode: selected.Mode, Data: data},
	}, nil
}

func goProxyEnvironment(proxy string) []string {
	env := withoutEnvironment(os.Environ(), "GOWORK", "GOPROXY", "GONOSUMDB", "GONOPROXY", "GOMODCACHE", "GOFLAGS")
	upstream := os.Getenv("GOPROXY")
	if upstream == "" {
		upstream = "https://proxy.golang.org,direct"
	}
	// Only bypass checksum verification for candidates served by this local proxy.
	env = append(env, "GOWORK=off", "GOPROXY=file://"+filepath.ToSlash(proxy)+","+upstream, "GONOPROXY=none", "GOMODCACHE="+filepath.Join(proxy, "cache"), "GOFLAGS="+os.Getenv("GOFLAGS")+" -modcacherw")
	var paths []string
	// Module checksums do not exist before tags; scope bypass to generated candidates.
	filepath.WalkDir(proxy, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == "cache" {
			return filepath.SkipDir
		}
		if err == nil && d.IsDir() && d.Name() == "@v" {
			relative, _ := filepath.Rel(proxy, filepath.Dir(path))
			decoded, _ := module.UnescapePath(filepath.ToSlash(relative))
			paths = append(paths, decoded)
		}
		return err
	})
	if configured := os.Getenv("GONOSUMDB"); configured != "" {
		paths = append(paths, configured)
	}
	env = append(env, "GONOSUMDB="+strings.Join(paths, ","))
	return env
}

func writeGoModuleProxy(proxy string, modules []GoModule, version string) error {
	for _, m := range modules {
		if m.Publication != "public" {
			continue
		}
		path, err := module.EscapePath(m.Path)
		if err != nil {
			return err
		}
		escapedVersion, err := module.EscapeVersion(version)
		if err != nil {
			return err
		}
		dir := filepath.Join(proxy, filepath.FromSlash(path), "@v")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		base := filepath.Join(dir, escapedVersion)
		data, err := os.ReadFile(filepath.Join(m.Directory, "go.mod"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(base+".mod", data, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(base+".info", []byte(fmt.Sprintf(`{"Version":%q,"Time":"2000-01-01T00:00:00Z"}`, version)), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "list"), []byte(version+"\n"), 0o644); err != nil {
			return err
		}
		file, err := os.Create(base + ".zip")
		if err != nil {
			return err
		}
		err = zip.CreateFromDir(file, module.Version{Path: m.Path, Version: version}, m.Directory)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func mergeGoWork(plan *GeneratePlan) error {
	m, found, err := ReadGoModule(plan.selectedStage)
	if err != nil || !found {
		return err
	}
	filename := filepath.Join(plan.workspaceRoot, "go.work")
	data, err := os.ReadFile(filename)
	selected := PathValue{Kind: "file", Mode: 0o644}
	work := &modfile.WorkFile{Syntax: &modfile.FileSyntax{}}
	if err == nil {
		selected.Present = true
		selected.Data = data
		info, statErr := os.Stat(filename)
		if statErr != nil {
			return statErr
		}
		selected.Mode = info.Mode().Perm()
		work, err = modfile.ParseWork(filename, data, nil)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	goVersion := "1.25.0"
	if m.File.Go != nil {
		goVersion = m.File.Go.Version
	}
	if work.Go == nil || version.Compare("go"+work.Go.Version, "go"+goVersion) < 0 {
		if err := work.AddGoStmt(goVersion); err != nil {
			return err
		}
	}
	// Preserve all existing use, replace, toolchain directives and comments.
	if err := work.AddUse("./"+filepath.ToSlash(plan.projectPath), m.Path); err != nil {
		return err
	}
	if root, rootModule, err := ReadGoModule(plan.workspaceRoot); err != nil {
		return err
	} else if rootModule {
		if root.File.Go != nil && version.Compare("go"+root.File.Go.Version, "go"+work.Go.Version) > 0 {
			if err := work.AddGoStmt(root.File.Go.Version); err != nil {
				return err
			}
		}
		if err := work.AddUse(".", ""); err != nil {
			return err
		}
	}
	work.SortBlocks()
	work.Cleanup()
	output := PathValue{Present: true, Kind: "file", Mode: selected.Mode, Data: modfile.Format(work.Syntax)}
	plan.Entries = append(plan.Entries, MergeEntry{Path: "go.work", Selected: selected, Output: output, Strategy: MergeStrategyCopy})
	return nil
}

func copyGoWorkModules(source, candidate string) error {
	data, err := os.ReadFile(filepath.Join(candidate, "go.work"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	work, err := modfile.ParseWork("go.work", data, nil)
	if err != nil {
		return err
	}
	for _, use := range work.Use {
		if filepath.IsAbs(use.Path) {
			return fmt.Errorf("Go workspace use %s must be relative for isolated generation", use.Path)
		}
		relative := filepath.Clean(use.Path)
		if relative != "." && !filepath.IsLocal(relative) {
			return fmt.Errorf("Go workspace use %s escapes workspace", use.Path)
		}
		directory := filepath.Join(source, relative)
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		destination := filepath.Join(candidate, relative)
		if relative == "." {
			entries, err := os.ReadDir(source)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "apps" || entry.Name() == "libs" || entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == "bin" {
					continue
				}
				target := filepath.Join(destination, entry.Name())
				if err := os.MkdirAll(target, 0o755); err != nil {
					return err
				}
				if err := copyTree(filepath.Join(source, entry.Name()), target, nil); err != nil {
					return err
				}
			}
			continue
		}
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return err
		}
		if err := copyTree(directory, destination, nil); err != nil {
			return err
		}
	}
	return nil
}

// goReleaseTool selects a Go tool for releases launched from a module-free
// workspace root. Prefer project Mise pins, then native toolchain directives.
func goReleaseTool(root string, manifest Config) (string, error) {
	modules, err := GoModules(root, manifest)
	if err != nil {
		return "", err
	}
	if m, found, err := ReadGoModule(root); err != nil {
		return "", err
	} else if found {
		modules = append(modules, m)
	}
	selected := ""
	for _, m := range modules {
		candidate := "1.25.0"
		if m.File.Go != nil {
			candidate = m.File.Go.Version
		}
		if m.File.Toolchain != nil && version.Compare(m.File.Toolchain.Name, "go"+candidate) > 0 {
			candidate = strings.TrimPrefix(m.File.Toolchain.Name, "go")
		}
		data, err := os.ReadFile(filepath.Join(m.Directory, "mise.toml"))
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil {
			config, err := ExtractMiseConfig("Go project", data)
			if err != nil {
				return "", err
			}
			if tool, ok := config.Tools["go"]; ok {
				if tool.StringSelector {
					candidate = tool.Selector
				} else if object, ok := tool.Raw.(map[string]any); ok {
					if pin, ok := object["version"].(string); ok {
						candidate = pin
					}
				}
			}
		}
		if selected == "" || version.Compare("go"+candidate, "go"+selected) > 0 {
			selected = candidate
		}
	}
	if selected == "" {
		return "", nil
	}
	return "go@" + selected, nil
}
