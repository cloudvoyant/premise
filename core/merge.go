package core

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type MergeStrategy string

const (
	MergeStrategyCopy         MergeStrategy = "copy"
	MergeStrategyEqual        MergeStrategy = "equal"
	MergeStrategyOrderedLines MergeStrategy = "ordered-lines"
	MergeStrategyMise         MergeStrategy = "mise"
	MergeStrategyWholeFile    MergeStrategy = "whole-file"
)

type MergeChoice string

const (
	MergeChoiceKeepShared     MergeChoice = "keep-shared"
	MergeChoiceUseSelected    MergeChoice = "use-selected"
	MergeChoiceRenameSelected MergeChoice = "rename-selected"
	MergeChoiceAbort          MergeChoice = "abort"
)

type PathValue struct {
	Present bool
	Kind    string
	Mode    fs.FileMode
	Data    []byte
}

type MergeConflict struct {
	Path        string
	Key         string
	Kind        string
	Shared      string
	Selected    string
	AllowRename bool
}

type MergeDecision struct {
	Choice MergeChoice
	Rename string
}

type MergeEntry struct {
	Path     string
	Shared   PathValue
	Selected PathValue
	Output   PathValue
	Strategy MergeStrategy
}

type TemplateMergePlan struct {
	Entries    []MergeEntry
	Collisions []MergeConflict
	Root       PathValue
	Mise       MiseMergeResult
}

type MergeResolver interface {
	ResolveMergeConflict(MergeConflict) (MergeDecision, error)
}

func BuildTemplateMergePlan(sharedRoot, selectedRoot string, replacements map[string]string, selectedIdentity string, resolver MergeResolver) (TemplateMergePlan, error) {
	shared, err := readSharedValues(sharedRoot, literalReplacer(replacements))
	if err != nil {
		return TemplateMergePlan{}, err
	}
	selected, rootMode, err := readTreeValues(selectedRoot, literalReplacer(replacements))
	if err != nil {
		return TemplateMergePlan{}, err
	}
	paths := make([]string, 0, len(shared)+len(selected))
	seen := make(map[string]struct{}, len(shared)+len(selected))
	for path := range shared {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range selected {
		if _, ok := seen[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	plan := TemplateMergePlan{
		Entries: make([]MergeEntry, 0, len(paths)),
		Root:    PathValue{Present: true, Kind: "dir", Mode: rootMode},
	}
	for _, path := range paths {
		sharedValue, sharedOK := shared[path]
		selectedValue, selectedOK := selected[path]
		sharedValue.Present = sharedOK
		selectedValue.Present = selectedOK
		entry := MergeEntry{Path: path, Shared: sharedValue, Selected: selectedValue}
		switch {
		case !sharedOK:
			entry.Output = clonePathValue(selectedValue)
			entry.Strategy = MergeStrategyCopy
		case !selectedOK:
			entry.Output = clonePathValue(sharedValue)
			entry.Strategy = MergeStrategyCopy
		default:
			conflict := MergeConflict{
				Path:     path,
				Key:      path,
				Kind:     "file",
				Shared:   describePathValue(sharedValue),
				Selected: describePathValue(selectedValue),
			}
			plan.Collisions = append(plan.Collisions, conflict)
			if sharedValue.Kind == selectedValue.Kind && sharedValue.Mode.Perm() == selectedValue.Mode.Perm() && bytes.Equal(sharedValue.Data, selectedValue.Data) {
				entry.Output = clonePathValue(selectedValue)
				entry.Strategy = MergeStrategyEqual
				break
			}
			if sharedValue.Kind == "file" && selectedValue.Kind == "file" {
				switch path {
				case ".gitignore", ".gitattributes":
					mode, err := resolveMergedMode(path, sharedValue, selectedValue, resolver)
					if err != nil {
						return TemplateMergePlan{}, err
					}
					entry.Output = PathValue{Present: true, Kind: "file", Mode: mode, Data: MergeOrderedPolicy(sharedValue.Data, selectedValue.Data)}
					entry.Strategy = MergeStrategyOrderedLines
					break
				case "mise.toml":
					mode, err := resolveMergedMode(path, sharedValue, selectedValue, resolver)
					if err != nil {
						return TemplateMergePlan{}, err
					}
					result, err := ComposeMise(sharedValue.Data, selectedValue.Data, selectedIdentity, resolver)
					if err != nil {
						return TemplateMergePlan{}, fmt.Errorf("merge %s: %w", path, err)
					}
					plan.Mise = result
					entry.Output = PathValue{Present: true, Kind: "file", Mode: mode, Data: append([]byte(nil), result.Bytes...)}
					entry.Strategy = MergeStrategyMise
					break
				}
				if entry.Strategy != "" {
					break
				}
			}
			decision, err := resolveConflict(resolver, conflict)
			if err != nil {
				return TemplateMergePlan{}, err
			}
			switch decision.Choice {
			case MergeChoiceKeepShared:
				entry.Output = clonePathValue(sharedValue)
			case MergeChoiceUseSelected:
				entry.Output = clonePathValue(selectedValue)
			case MergeChoiceAbort:
				return TemplateMergePlan{}, fmt.Errorf("merge aborted for %s", path)
			default:
				return TemplateMergePlan{}, fmt.Errorf("invalid merge choice %q for %s", decision.Choice, path)
			}
			entry.Strategy = MergeStrategyWholeFile
		}
		plan.Entries = append(plan.Entries, entry)
	}
	plan.Entries = pruneOutputFileDescendants(plan.Entries)
	return plan, nil
}

func pruneOutputFileDescendants(entries []MergeEntry) []MergeEntry {
	output := make([]MergeEntry, 0, len(entries))
	for _, entry := range entries {
		prune := false
		for _, ancestor := range output {
			if ancestor.Output.Kind == "file" && strings.HasPrefix(entry.Path, ancestor.Path+"/") {
				prune = true
				break
			}
		}
		if !prune {
			output = append(output, entry)
		}
	}
	return output
}

func MergeOrderedPolicy(shared, selected []byte) []byte {
	normalize := func(data []byte) []string {
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		text = strings.TrimSuffix(text, "\n")
		if text == "" {
			return nil
		}
		return strings.Split(text, "\n")
	}
	lines := append(normalize(shared), normalize(selected)...)
	merged := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(merged) == 0 || merged[len(merged)-1] != line {
			merged = append(merged, line)
		}
	}
	return []byte(strings.Join(merged, "\n") + "\n")
}

func MaterializeTemplateMerge(plan TemplateMergePlan, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("create merge destination: %w", err)
	}
	directories := make([]MergeEntry, 0)
	for _, entry := range plan.Entries {
		target := filepath.Join(destination, filepath.FromSlash(entry.Path))
		if !inside(destination, target) {
			return fmt.Errorf("merge destination entry escapes scaffold root: %s", target)
		}
		if entry.Output.Kind == "dir" {
			if err := os.MkdirAll(target, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("create scaffold directory %s: %w", entry.Path, err)
			}
			directories = append(directories, entry)
			continue
		}
		if entry.Output.Kind != "file" {
			return fmt.Errorf("unsupported output kind %q for %s", entry.Output.Kind, entry.Path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create scaffold parent %s: %w", entry.Path, err)
		}
		if err := os.WriteFile(target, entry.Output.Data, entry.Output.Mode.Perm()); err != nil {
			return fmt.Errorf("write scaffold file %s: %w", entry.Path, err)
		}
		if err := os.Chmod(target, entry.Output.Mode.Perm()); err != nil {
			return fmt.Errorf("preserve scaffold file permissions %s: %w", entry.Path, err)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.Count(directories[i].Path, "/") > strings.Count(directories[j].Path, "/")
	})
	for _, entry := range directories {
		if err := os.Chmod(filepath.Join(destination, filepath.FromSlash(entry.Path)), entry.Output.Mode.Perm()); err != nil {
			return fmt.Errorf("preserve scaffold directory permissions %s: %w", entry.Path, err)
		}
	}
	if plan.Root.Present {
		if err := os.Chmod(destination, plan.Root.Mode.Perm()); err != nil {
			return fmt.Errorf("preserve scaffold root permissions: %w", err)
		}
	}
	return nil
}

func readSharedValues(root string, replacer *strings.Replacer) (map[string]PathValue, error) {
	values := make(map[string]PathValue)
	if root == "" {
		return values, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read shared scaffold source: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect shared scaffold entry %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("unsupported shared scaffold entry %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read shared scaffold file %s: %w", entry.Name(), err)
		}
		if replacer != nil && isText(data) {
			data = []byte(replacer.Replace(string(data)))
		}
		values[filepath.ToSlash(entry.Name())] = PathValue{Kind: "file", Mode: info.Mode().Perm(), Data: data}
	}
	return values, nil
}

func readTreeValues(root string, replacer *strings.Replacer) (map[string]PathValue, fs.FileMode, error) {
	values := make(map[string]PathValue)
	var rootMode fs.FileMode
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || escapesRoot(relative) {
			return fmt.Errorf("source entry escapes scaffold root: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect scaffold entry %s: %w", path, err)
		}
		if relative == "." {
			if !info.IsDir() {
				return fmt.Errorf("scaffold root %s is not a directory", path)
			}
			rootMode = info.Mode().Perm()
			return nil
		}
		key := filepath.ToSlash(relative)
		if info.IsDir() {
			values[key] = PathValue{Kind: "dir", Mode: info.Mode().Perm()}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported scaffold entry %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read scaffold file %s: %w", relative, err)
		}
		if replacer != nil && isText(data) {
			data = []byte(replacer.Replace(string(data)))
		}
		values[key] = PathValue{Kind: "file", Mode: info.Mode().Perm(), Data: data}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("read template tree: %w", err)
	}
	return values, rootMode, nil
}

func resolveMergedMode(path string, shared, selected PathValue, resolver MergeResolver) (fs.FileMode, error) {
	if shared.Mode.Perm() == selected.Mode.Perm() {
		return selected.Mode.Perm(), nil
	}
	decision, err := resolveConflict(resolver, MergeConflict{
		Path: path, Key: path + ":mode", Kind: "mode",
		Shared: fmt.Sprintf("%#o", shared.Mode.Perm()), Selected: fmt.Sprintf("%#o", selected.Mode.Perm()),
	})
	if err != nil {
		return 0, err
	}
	switch decision.Choice {
	case MergeChoiceKeepShared:
		return shared.Mode.Perm(), nil
	case MergeChoiceUseSelected:
		return selected.Mode.Perm(), nil
	case MergeChoiceAbort:
		return 0, fmt.Errorf("merge aborted for %s mode", path)
	default:
		return 0, fmt.Errorf("invalid mode choice %q for %s", decision.Choice, path)
	}
}

func resolveConflict(resolver MergeResolver, conflict MergeConflict) (MergeDecision, error) {
	if resolver == nil {
		return MergeDecision{}, fmt.Errorf("merge decision required for %s (%s)", conflict.Path, conflict.Kind)
	}
	decision, err := resolver.ResolveMergeConflict(conflict)
	if err != nil {
		return MergeDecision{}, fmt.Errorf("resolve merge conflict %s: %w", conflict.Path, err)
	}
	return decision, nil
}

func clonePathValue(value PathValue) PathValue {
	value.Present = true
	value.Data = append([]byte(nil), value.Data...)
	return value
}

func describePathValue(value PathValue) string {
	if value.Kind == "dir" {
		return fmt.Sprintf("directory mode %#o", value.Mode.Perm())
	}
	const limit = 160
	text := strings.TrimSpace(string(value.Data))
	if len(text) > limit {
		text = text[:limit] + "…"
	}
	if text == "" {
		text = fmt.Sprintf("%d bytes", len(value.Data))
	}
	return text
}
