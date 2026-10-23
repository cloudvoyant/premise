package backends

import (
	"fmt"
	"go/version"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudvoyant/premise/core"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Private helpers ------------------------------------------------------------

type goModule struct {
	Path string
	File *modfile.File
}

func readGoModule(directory string) (goModule, bool, error) {
	data, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if os.IsNotExist(err) {
		return goModule{}, false, nil
	}
	if err != nil {
		return goModule{}, false, err
	}
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return goModule{}, false, err
	}
	if file.Module == nil {
		return goModule{}, false, fmt.Errorf("%s/go.mod has no module directive", directory)
	}
	if err := module.CheckPath(file.Module.Mod.Path); err != nil {
		return goModule{}, false, err
	}
	return goModule{Path: file.Module.Mod.Path, File: file}, true, nil
}

// prepareGoWorkspace promotes a template's dependencies into the shared root
// manifest. Generated projects contain packages, not nested Go modules.
func prepareGoWorkspace(request core.PackageGenerationContext) ([]core.MergeEntry, error) {
	source, found, err := readGoModule(request.Stage)
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
	existing, exists, err := readGoModule(request.WorkspaceRoot)
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
