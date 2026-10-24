package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Private helpers ------------------------------------------------------------

type generationProvider struct {
	id      string
	backend PackageManagerGeneration
	paths   []string
}

func prepareNativeGeneration(plan *GeneratePlan, request GenerateParameters, shared map[string]PathValue) error {
	backends, err := packageManagersForConfig(Config{Workspace: Workspace{PackageManagers: request.PackageManagers}})
	if err != nil {
		return err
	}
	owners := map[string]string{}
	var providers []generationProvider
	for _, backend := range backends {
		generation, ok := backend.(PackageManagerGeneration)
		if !ok {
			continue
		}
		paths := generation.GenerationRootFiles()
		for _, path := range paths {
			if path == "" || path == "." || filepath.Base(path) != path || strings.ContainsAny(path, "/\\") || path == ManifestFilename || path == "mise.toml" || path == ".git" || path == ".difflab" {
				return fmt.Errorf("package manager %s claims invalid native root path %q", backend.ID(), path)
			}
			if owner, exists := owners[path]; exists {
				return fmt.Errorf("native root %s is owned by both %s and %s", path, owner, backend.ID())
			}
			owners[path] = backend.ID()
		}
		providers = append(providers, generationProvider{id: backend.ID(), backend: generation, paths: paths})
	}
	paths := make([]string, 0, len(owners))
	for path := range owners {
		paths = append(paths, path)
	}
	originals, err := readClientRootValues(plan.workspaceRoot, paths)
	if err != nil {
		return err
	}
	plan.nativeRoots = make(map[string]PathValue, len(paths))
	for _, path := range paths {
		original, exists := originals[path]
		if exists && original.Kind != "file" {
			return fmt.Errorf("native root %s must be a regular file", path)
		}
		if !exists {
			original = PathValue{Kind: "file", Mode: 0644}
		}
		plan.nativeRoots[path] = original
	}
	for _, provider := range providers {
		clientFiles, sharedFiles := map[string]PathValue{}, map[string]PathValue{}
		for _, path := range provider.paths {
			original := plan.nativeRoots[path]
			original.Data = append([]byte(nil), original.Data...)
			clientFiles[path] = original
			if value, exists := shared[path]; exists {
				if value.Kind != "file" {
					return fmt.Errorf("shared native root %s must be a regular file", path)
				}
				value.Data = append([]byte(nil), value.Data...)
				sharedFiles[path] = value
			}
		}
		entries, err := provider.backend.PrepareGeneration(PackageGenerationContext{
			WorkspaceRoot: plan.workspaceRoot, WorkspaceName: request.WorkspaceName,
			ProjectPath: plan.projectPath, ProjectPaths: append([]string(nil), request.ProjectPaths...), Stage: plan.selectedStage,
			ClientFiles: clientFiles, SharedFiles: sharedFiles, ResolveConflict: func(conflict MergeConflict) (MergeDecision, error) {
				return resolveConflict(request.ResolveConflict, conflict)
			},
		})
		if err != nil {
			return fmt.Errorf("prepare %s workspace: %w", provider.id, err)
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			if owners[entry.Path] != provider.id {
				return fmt.Errorf("package manager %s returned unclaimed root %q", provider.id, entry.Path)
			}
			if seen[entry.Path] {
				return fmt.Errorf("package manager %s returned duplicate root %s", provider.id, entry.Path)
			}
			seen[entry.Path] = true
			if entry.Output.Present && entry.Output.Kind != "file" {
				return fmt.Errorf("native root %s output must be a regular file", entry.Path)
			}
			entry.Selected = plan.nativeRoots[entry.Path]
			entry.Shared = sharedFiles[entry.Path]
			entry.Output.Mode = entry.Selected.Mode
			plan.Entries = append(plan.Entries, entry)
		}
	}
	return nil
}

func (plan *GeneratePlan) captureNativeOutputs(candidate string) error {
	paths := unionPathKeys(plan.nativeRoots, nil)
	outputs, err := readClientRootValues(candidate, paths)
	if err != nil {
		return err
	}
	indexes := map[string]int{}
	for index, entry := range plan.Entries {
		indexes[entry.Path] = index
	}
	for _, path := range paths {
		original := plan.nativeRoots[path]
		output := outputs[path]
		if output.Present && output.Kind != "file" {
			return fmt.Errorf("native root %s output must be a regular file", path)
		}
		output.Mode = original.Mode
		if index, exists := indexes[path]; exists {
			plan.Entries[index].Output = output
		} else if output.Present != original.Present || output.Present && !samePathValue(output, original) {
			plan.Entries = append(plan.Entries, MergeEntry{Path: path, Selected: original, Output: output, Strategy: MergeStrategyCopy})
		}
	}
	sort.Slice(plan.Entries, func(i, j int) bool { return plan.Entries[i].Path < plan.Entries[j].Path })
	return nil
}

func copyGenerationWorkspace(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative != "." && (excludedGenerationEntry(source, relative) || inside(destination, path)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported workspace source entry %s: local sources must be regular files or directories", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read workspace source %s: %w", relative, err)
		}
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chmod(target, info.Mode().Perm())
	})
}

func excludedGenerationEntry(source, relative string) bool {
	name := filepath.Base(relative)
	switch name {
	case ".git", ".difflab", "node_modules", ".cache", ".pulumi", ".pulumi-state", ".vercel", ".output", ".venv", "__pycache__":
		return true
	case "target", "bin", "dist", "coverage":
		// An authored project can itself have an output-like name.
		if _, err := os.Stat(filepath.Join(source, relative, "mise.toml")); err == nil {
			return false
		}
		if relative == name {
			return true
		}
		_, err := os.Stat(filepath.Join(source, filepath.Dir(relative), "mise.toml"))
		return err == nil
	}
	return strings.HasPrefix(name, ".premise-stage-") || strings.HasPrefix(name, "premise-selected-") || strings.HasPrefix(name, "premise-candidate-validation-") || strings.HasPrefix(name, "premise-tool-preflight-")
}
