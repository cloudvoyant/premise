package backends

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/cloudvoyant/premise/core"
)

// Public API -----------------------------------------------------------------

func (Bun) GenerationRootFiles() []string { return []string{"package.json", "bun.lock", "bun.lockb"} }

func (Bun) PrepareGeneration(request core.PackageGenerationContext) ([]core.MergeEntry, error) {
	selectedData, err := os.ReadFile(filepath.Join(request.Stage, "package.json"))
	selected := err == nil
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	shared := request.SharedFiles["package.json"]
	if !selected && !shared.Present {
		return nil, nil
	}
	if selected {
		project, err := readGenerationJSON(selectedData)
		if err != nil {
			return nil, fmt.Errorf("selected package.json: %w", err)
		}
		if err := validateBunGenerationPaths(request.WorkspaceRoot, request.ProjectPath, project); err != nil {
			return nil, err
		}
	}
	original := request.ClientFiles["package.json"]
	client, err := readGenerationJSON(original.Data)
	if err != nil {
		return nil, fmt.Errorf("client package.json: %w", err)
	}
	incoming, err := readGenerationJSON(shared.Data)
	if err != nil {
		return nil, fmt.Errorf("shared package.json: %w", err)
	}
	// Registry identity and registry-development membership are not client contributions.
	for _, key := range []string{"name", "version", "private", "workspaces"} {
		delete(incoming, key)
	}
	if !original.Present {
		name := request.WorkspaceName
		if name == "" {
			name = filepath.Base(request.WorkspaceRoot)
		}
		client = map[string]any{"name": name, "private": true, "workspaces": []any{"apps/*", "libs/*"}}
	}
	if err := mergeGenerationJSON(client, incoming, "package.json", request.ResolveConflict); err != nil {
		return nil, err
	}
	patterns, err := bunGenerationPatterns(client)
	if err != nil {
		return nil, err
	}
	explicit := slices.Contains(patterns, request.ProjectPath) || slices.Contains(patterns, "./"+request.ProjectPath)
	covered := false
	for _, pattern := range patterns {
		negative := strings.HasPrefix(pattern, "!")
		raw := strings.TrimPrefix(pattern, "!")
		if err := validateGenerationLocalPath(request.WorkspaceRoot, "", raw); err != nil {
			return nil, err
		}
		matches, err := generationPatternMatches(raw, request.ProjectPath)
		if err != nil {
			return nil, err
		}
		// Bun's dir/** exclusion also removes dir itself.
		if negative && strings.HasSuffix(raw, "/**") && strings.TrimSuffix(raw, "/**") == request.ProjectPath {
			matches = true
		}
		if matches {
			if negative && !explicit {
				return nil, fmt.Errorf("package.json.workspaces excludes generated project %s with %q", request.ProjectPath, pattern)
			}
			covered = true
		}
	}
	if selected && !covered {
		patterns = append(patterns, request.ProjectPath)
		values := make([]any, len(patterns))
		for i, pattern := range patterns {
			values[i] = pattern
		}
		if object, ok := client["workspaces"].(map[string]any); ok {
			object["packages"] = values
		} else {
			client["workspaces"] = values
		}
	}
	if err := validateBunGenerationPaths(request.WorkspaceRoot, "", client); err != nil {
		return nil, err
	}
	output, err := json.MarshalIndent(client, "", "  ")
	if err != nil {
		return nil, err
	}
	return []core.MergeEntry{generationEntry("package.json", append(output, '\n'))}, nil
}

// Private helpers ------------------------------------------------------------

func readGenerationJSON(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return value, nil
	} else if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("unexpected content after JSON object")
}

func mergeGenerationJSON(client, incoming map[string]any, path string, resolver core.MergeConflictResolver) error {
	keys := make([]string, 0, len(incoming))
	for key := range incoming {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		next := incoming[key]
		previous, exists := client[key]
		if !exists {
			client[key] = next
			continue
		}
		if reflect.DeepEqual(previous, next) {
			continue
		}
		left, leftMap := previous.(map[string]any)
		right, rightMap := next.(map[string]any)
		if leftMap && rightMap {
			if err := mergeGenerationJSON(left, right, path+"."+key, resolver); err != nil {
				return err
			}
			continue
		}
		conflict := core.MergeConflict{Path: "package.json", Key: path + "." + key, Kind: "native value", Shared: fmt.Sprint(next), Selected: fmt.Sprint(previous)}
		if resolver == nil {
			return fmt.Errorf("merge conflict for %s requires a resolver", conflict.Key)
		}
		decision, err := resolver(conflict)
		if err != nil {
			return err
		}
		switch decision.Choice {
		case core.MergeChoiceKeepShared:
			client[key] = next
		case core.MergeChoiceUseSelected:
		case core.MergeChoiceAbort:
			return fmt.Errorf("merge aborted for %s", conflict.Key)
		default:
			return fmt.Errorf("invalid merge choice %q for %s", decision.Choice, conflict.Key)
		}
	}
	return nil
}

func bunGenerationPatterns(value map[string]any) ([]string, error) {
	raw, exists := value["workspaces"]
	if !exists {
		return nil, nil
	}
	if object, ok := raw.(map[string]any); ok {
		raw = object["packages"]
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("package.json.workspaces must be an array or an object with a packages array")
	}
	patterns := make([]string, 0, len(entries))
	for _, entry := range entries {
		pattern, ok := entry.(string)
		if !ok || pattern == "" {
			return nil, fmt.Errorf("package.json.workspaces entries must be nonempty strings")
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func validateBunGenerationPaths(root, base string, pkg map[string]any) error {
	for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies"} {
		dependencies, _ := pkg[field].(map[string]any)
		for _, raw := range dependencies {
			value, ok := raw.(string)
			if !ok {
				continue
			}
			for _, prefix := range []string{"file:", "link:", "workspace:"} {
				if !strings.HasPrefix(value, prefix) {
					continue
				}
				path := strings.TrimPrefix(value, prefix)
				if prefix == "workspace:" && !strings.HasPrefix(path, ".") && !filepath.IsAbs(path) {
					continue
				}
				if err := validateGenerationLocalPath(root, base, path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
