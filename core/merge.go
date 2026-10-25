package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"

	"charm.land/huh/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
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

// MergeJSON combines JSON object fields. Arrays keep distinct items in order;
// conflicting scalars use the normal resolver. Dependency versions use the
// separate MergeDependencies policy.
func MergeJSON(path string, shared, selected []byte, resolver MergeConflictResolver) ([]byte, error) {
	decode := func(data []byte, value any) error {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		return decodeSingleDocument(decoder.Decode, value)
	}
	encode := func(value any) ([]byte, error) {
		data, err := json.MarshalIndent(value, "", "  ")
		return append(data, '\n'), err
	}
	return mergeDocuments(path, shared, selected, decode, encode, resolver)
}

// MergeTOML applies the same object, array, and scalar policy to TOML values.
// Serialization preserves values rather than source formatting or comments.
func MergeTOML(path string, shared, selected []byte, resolver MergeConflictResolver) ([]byte, error) {
	return mergeDocuments(path, shared, selected, toml.Unmarshal, toml.Marshal, resolver)
}

// MergeYAML applies the same policy to a single YAML mapping document.
func MergeYAML(path string, shared, selected []byte, resolver MergeConflictResolver) ([]byte, error) {
	decode := func(data []byte, value any) error {
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		return decodeSingleDocument(decoder.Decode, value)
	}
	return mergeDocuments(path, shared, selected, decode, yaml.Marshal, resolver)
}

// MergeManifestDependencies runs the same parse, merge, and write sequence for
// every native manifest. The selected manifest supplies unrelated settings.
func MergeManifestDependencies(backend PackageManagerBackend, path string, shared, selected []byte, resolver MergeConflictResolver) ([]byte, error) {
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

func decodeSingleDocument(decode func(any) error, value any) error {
	if err := decode(value); err != nil {
		return err
	}
	if err := decode(new(any)); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("expected one document")
	}
	return nil
}

func mergeDocuments(path string, shared, selected []byte, decode func([]byte, any) error, encode func(any) ([]byte, error), resolver MergeConflictResolver) ([]byte, error) {
	parse := func(data []byte) (map[string]any, error) {
		value := map[string]any{}
		if len(bytes.TrimSpace(data)) == 0 {
			return value, nil
		}
		if err := decode(data, &value); err != nil {
			return nil, err
		}
		if value == nil {
			return nil, fmt.Errorf("expected an object")
		}
		return value, nil
	}
	incoming, err := parse(shared)
	if err != nil {
		return nil, fmt.Errorf("parse shared %s: %w", path, err)
	}
	client, err := parse(selected)
	if err != nil {
		return nil, fmt.Errorf("parse client %s: %w", path, err)
	}
	merged, _, err := mergeDocumentValue(path, "", incoming, true, client, true, resolver)
	if err != nil {
		return nil, err
	}
	return encode(merged)
}

func mergeDocumentValue(file, path string, left any, leftOK bool, right any, rightOK bool, resolver MergeConflictResolver) (any, bool, error) {
	if !leftOK {
		return cloneManifestValue(right), rightOK, nil
	}
	if !rightOK {
		return cloneManifestValue(left), true, nil
	}
	if reflect.DeepEqual(left, right) {
		return cloneManifestValue(right), true, nil
	}
	leftMap, leftMapOK := left.(map[string]any)
	rightMap, rightMapOK := right.(map[string]any)
	if leftMapOK && rightMapOK {
		output := make(map[string]any)
		for _, key := range sortedUnionKeys(leftMap, rightMap) {
			value, ok, err := mergeDocumentValue(file, strings.TrimPrefix(path+"."+key, "."), leftMap[key], hasKey(leftMap, key), rightMap[key], hasKey(rightMap, key), resolver)
			if err != nil {
				return nil, false, err
			}
			if ok {
				output[key] = value
			}
		}
		return output, true, nil
	}
	leftList, leftListOK := anyList(left)
	rightList, rightListOK := anyList(right)
	if leftListOK && rightListOK {
		output := make([]any, 0, len(leftList)+len(rightList))
		for _, item := range append(leftList, rightList...) {
			found := false
			for _, existing := range output {
				if reflect.DeepEqual(existing, item) {
					found = true
					break
				}
			}
			if !found {
				output = append(output, cloneManifestValue(item))
			}
		}
		return output, true, nil
	}
	value, err := resolveDocumentScalar(file, path, left, right, resolver)
	return value, true, err
}

func resolveDocumentScalar(file, path string, shared, selected any, resolver MergeConflictResolver) (any, error) {
	conflict := MergeConflict{
		Path:     file,
		Key:      path,
		Kind:     "manifest value",
		Shared:   fmt.Sprint(shared),
		Selected: fmt.Sprint(selected),
	}
	decision, err := resolveConflict(resolver, conflict)
	if err != nil {
		return nil, err
	}
	switch decision.Choice {
	case MergeChoiceKeepShared:
		return cloneManifestValue(shared), nil
	case MergeChoiceUseSelected:
		return cloneManifestValue(selected), nil
	case MergeChoiceAbort:
		return nil, fmt.Errorf("merge aborted for manifest value %s", path)
	default:
		return nil, fmt.Errorf("invalid manifest merge choice %q for %s", decision.Choice, path)
	}
}

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

func anyList(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []string:
		output := make([]any, len(typed))
		for index, item := range typed {
			output[index] = item
		}
		return output, true
	default:
		return nil, false
	}
}

func sortedUnionKeys(left, right map[string]any) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		seen[key] = struct{}{}
	}
	for key := range right {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hasKey(values map[string]any, key string) bool {
	_, ok := values[key]
	return ok
}
