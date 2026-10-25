package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"

	"charm.land/huh/v2"
	"github.com/Masterminds/semver/v3"
)

// Public API -----------------------------------------------------------------

// MergeStrategy describes how a colliding path is resolved.
type MergeStrategy string

const (
	// MergeStrategyCopy is a one-sided copy: only one input contains the path.
	MergeStrategyCopy MergeStrategy = "copy"
	// MergeStrategyEqual is equal coalescing: both inputs contain the same path.
	MergeStrategyEqual MergeStrategy = "equal"
	// MergeStrategyMise is a tier-one typed merge of the root mise.toml.
	MergeStrategyMise MergeStrategy = "mise"
	// MergeStrategyOrderedLines is a tier-two ordered-line merge of ignore files.
	MergeStrategyOrderedLines MergeStrategy = "ordered-lines"
	// MergeStrategyWholeFile is a tier-three whole-file conflict selection.
	MergeStrategyWholeFile MergeStrategy = "whole-file"
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

// MergeConflictResolver is an optional deterministic conflict decision
// callback. A nil callback selects the production interactive prompt.
type MergeConflictResolver func(MergeConflict) (MergeDecision, error)

// MergeDecisions resolves conflicts deterministically by conflict key, path,
// and finally aborting when no decision is supplied.
type MergeDecisions map[string]MergeDecision

// Resolve implements MergeConflictResolver for a fixed decision set.
func (decisions MergeDecisions) Resolve(conflict MergeConflict) (MergeDecision, error) {
	if decision, ok := decisions[conflict.Key]; ok {
		return decision, nil
	}
	if decision, ok := decisions[conflict.Path]; ok {
		return decision, nil
	}
	return MergeDecision{Choice: MergeChoiceAbort}, nil
}

type MergeEntry struct {
	Path     string
	Shared   PathValue
	Selected PathValue
	Output   PathValue
	Strategy MergeStrategy
}

// MergeManifestDependencies runs the same parse, merge, and write sequence for
// every native manifest. The selected manifest supplies unrelated settings.
func MergeManifestDependencies(backend PackageManagerDependencies, path string, shared, selected []byte, resolver MergeConflictResolver) ([]byte, error) {
	incoming, err := backend.ParseDependencies(shared)
	if err != nil {
		return nil, fmt.Errorf("parse shared %s dependencies: %w", path, err)
	}
	existing, err := backend.ParseDependencies(selected)
	if err != nil {
		return nil, fmt.Errorf("parse client %s dependencies: %w", path, err)
	}
	merged, err := MergeDependencies(path, incoming, existing, resolver)
	if err != nil {
		return nil, fmt.Errorf("merge %s dependencies: %w", path, err)
	}
	return backend.WriteDependencies(selected, merged)
}

// MergeDependencies combines dependency lists by scope and name. One-sided
// dependencies are copied, compatible versions are upgraded, and native option
// conflicts require a decision. Direct requirements remain direct.
func MergeDependencies(path string, shared, selected []Dependency, resolver MergeConflictResolver) ([]Dependency, error) {
	output := map[string]Dependency{}
	for _, dependencies := range [][]Dependency{selected, shared} {
		dependencies = append([]Dependency(nil), dependencies...)
		slices.SortFunc(dependencies, func(left, right Dependency) int {
			return strings.Compare(dependencyIdentity(left), dependencyIdentity(right))
		})
		seen := map[string]bool{}
		for _, dependency := range dependencies {
			key := dependencyIdentity(dependency)
			if dependency.Name == "" || seen[key] {
				return nil, fmt.Errorf("%s has an empty or duplicate dependency %q", path, dependency.Name)
			}
			seen[key] = true
			dependency.Scope = append([]string(nil), dependency.Scope...)
			dependency.Options = cloneManifestValue(dependency.Options)
			previous, found := output[key]
			if !found {
				output[key] = dependency
				continue
			}
			merged, err := mergeDependency(path, dependency, previous, resolver)
			if err != nil {
				return nil, err
			}
			output[key] = merged
		}
	}
	keys := make([]string, 0, len(output))
	for key := range output {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	merged := make([]Dependency, 0, len(keys))
	for _, key := range keys {
		merged = append(merged, output[key])
	}
	return merged, nil
}

// Private helpers ------------------------------------------------------------

func cloneManifestValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := maps.Clone(typed)
		for key, item := range cloned {
			cloned[key] = cloneManifestValue(item)
		}
		return cloned
	case []any:
		cloned := slices.Clone(typed)
		for index, item := range cloned {
			cloned[index] = cloneManifestValue(item)
		}
		return cloned
	case []string:
		return slices.Clone(typed)
	default:
		return typed
	}
}

func dependencyIdentity(dependency Dependency) string {
	identity, _ := json.Marshal(append(append([]string(nil), dependency.Scope...), dependency.Name))
	return string(identity)
}

func mergeDependency(path string, shared, selected Dependency, resolver MergeConflictResolver) (Dependency, error) {
	conflict := MergeConflict{
		Path:     path,
		Key:      strings.Join(append(append([]string(nil), shared.Scope...), shared.Name), "."),
		Kind:     "dependency",
		Shared:   shared.Version,
		Selected: selected.Version,
	}
	if reflect.DeepEqual(shared.Options, selected.Options) {
		selected.Indirect = shared.Indirect && selected.Indirect
		shared.Indirect = selected.Indirect
		if shared.Version == selected.Version {
			return selected, nil
		}
		left, leftErr := semver.NewVersion(shared.Version)
		right, rightErr := semver.NewVersion(selected.Version)
		if leftErr == nil && rightErr == nil {
			if left.Major() != right.Major() {
				return Dependency{}, fmt.Errorf("%s has incompatible major versions %s and %s", conflict.Key, shared.Version, selected.Version)
			}
			if left.GreaterThan(right) {
				selected.Version = shared.Version
			}
			return selected, nil
		}
	} else {
		conflict.Kind = "dependency settings"
		conflict.Shared = fmt.Sprintf("%s %v", shared.Version, shared.Options)
		conflict.Selected = fmt.Sprintf("%s %v", selected.Version, selected.Options)
	}
	decision, err := resolveConflict(resolver, conflict)
	if err != nil {
		return Dependency{}, err
	}
	switch decision.Choice {
	case MergeChoiceKeepShared:
		return shared, nil
	case MergeChoiceUseSelected:
		return selected, nil
	case MergeChoiceAbort:
		return Dependency{}, fmt.Errorf("merge aborted for %s", conflict.Key)
	default:
		return Dependency{}, fmt.Errorf("invalid dependency merge choice %q", decision.Choice)
	}
}

func mergeTierOneMise(entry *MergeEntry, shared, selected PathValue, kind, identity string, resolver MergeConflictResolver) (MiseMergeResult, error) {
	result, err := composeMise(shared.Data, selected.Data, kind, identity, resolver)
	if err != nil {
		return MiseMergeResult{}, fmt.Errorf("merge mise.toml: %w", err)
	}
	mode, err := resolveMergedMode(entry.Path, shared, selected, resolver)
	if err != nil {
		return MiseMergeResult{}, err
	}
	entry.Output = PathValue{Present: true, Kind: "file", Mode: mode, Data: append([]byte(nil), result.Bytes...)}
	entry.Strategy = MergeStrategyMise
	return result, nil
}

func mergeTierTwoLines(entry *MergeEntry, shared, selected PathValue, resolver MergeConflictResolver) error {
	mode, err := resolveMergedMode(entry.Path, shared, selected, resolver)
	if err != nil {
		return err
	}
	entry.Output = PathValue{Present: true, Kind: "file", Mode: mode, Data: mergeOrderedLines(shared.Data, selected.Data)}
	entry.Strategy = MergeStrategyOrderedLines
	return nil
}

func mergeTierThreeWholeFile(entry *MergeEntry, conflict MergeConflict, resolver MergeConflictResolver) error {
	decision, err := resolveConflict(resolver, conflict)
	if err != nil {
		return err
	}
	switch decision.Choice {
	case MergeChoiceKeepShared:
		entry.Output = clonePathValue(entry.Shared)
	case MergeChoiceUseSelected:
		entry.Output = clonePathValue(entry.Selected)
	case MergeChoiceAbort:
		return fmt.Errorf("merge aborted for %s", conflict.Path)
	default:
		return fmt.Errorf("invalid merge choice %q for %s", decision.Choice, conflict.Path)
	}
	entry.Strategy = MergeStrategyWholeFile
	return nil
}

func mergeOrderedLines(shared, selected []byte) []byte {
	normalize := func(data []byte) []string {
		text := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
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

func isOrderedMergePath(path string) bool {
	return path == ".gitignore" || path == ".gitattributes"
}

func resolveMergedMode(path string, shared, selected PathValue, resolver MergeConflictResolver) (fs.FileMode, error) {
	if shared.Mode.Perm() == selected.Mode.Perm() {
		return selected.Mode.Perm(), nil
	}
	decision, err := resolveConflict(resolver, MergeConflict{Path: path, Key: path + ":mode", Kind: "mode", Shared: fmt.Sprintf("%#o", shared.Mode.Perm()), Selected: fmt.Sprintf("%#o", selected.Mode.Perm())})
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

func resolveConflict(resolver MergeConflictResolver, conflict MergeConflict) (MergeDecision, error) {
	if resolver == nil {
		resolver = promptMergeConflict
	}
	decision, err := resolver(conflict)
	if err != nil {
		return MergeDecision{}, fmt.Errorf("resolve merge conflict %s: %w", conflict.Path, err)
	}
	return decision, nil
}

func promptMergeConflict(conflict MergeConflict) (MergeDecision, error) {
	options := []huh.Option[MergeChoice]{
		huh.NewOption("Keep shared", MergeChoiceKeepShared),
		huh.NewOption("Use selected", MergeChoiceUseSelected),
	}
	if conflict.AllowRename {
		options = append(options, huh.NewOption("Rename selected", MergeChoiceRenameSelected))
	}
	options = append(options, huh.NewOption("Abort generation", MergeChoiceAbort))
	choice := MergeChoiceAbort
	title := fmt.Sprintf("Resolve %s conflict for %s (%s vs %s)", conflict.Kind, conflict.Key, conflict.Shared, conflict.Selected)
	if err := huh.NewSelect[MergeChoice]().Title(title).Options(options...).Value(&choice).Run(); err != nil {
		return MergeDecision{}, err
	}
	decision := MergeDecision{Choice: choice}
	if choice == MergeChoiceRenameSelected {
		var name string
		if err := huh.NewInput().Title("Rename selected " + conflict.Key + " to:").Value(&name).Validate(requiredAnswer).Run(); err != nil {
			return MergeDecision{}, err
		}
		decision.Rename = strings.TrimSpace(name)
	}
	return decision, nil
}

func clonePathValue(value PathValue) PathValue {
	value.Present = true
	value.Data = append([]byte(nil), value.Data...)
	return value
}

func samePathValue(left, right PathValue) bool {
	return left.Kind == right.Kind && left.Mode.Perm() == right.Mode.Perm() && bytes.Equal(left.Data, right.Data)
}
func pathKind(left, right PathValue) string {
	if left.Kind == right.Kind {
		return left.Kind
	}
	return left.Kind + "/" + right.Kind
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
