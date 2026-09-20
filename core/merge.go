package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/huh/v2"
)

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

type MergeEntry struct {
	Path     string
	Shared   PathValue
	Selected PathValue
	Output   PathValue
	Strategy MergeStrategy
}

// MergeRequest contains roots which have already been resolved by generation.
type MergeRequest struct {
	SharedRegistryRoot   string
	SelectedTemplateRoot string
	Destination          string
	Substitutions        map[string]string
	TemplateKind         string
	RegistryIdentity     string
	SelectedIdentity     string
	ResolveConflict      MergeConflictResolver
}

// MergePlan is the complete, prepared merge. Its staging paths are private so
// callers can only publish a plan through ExecuteMergePlan.
type MergePlan struct {
	Entries    []MergeEntry
	Collisions []MergeConflict
	Root       PathValue
	Mise       MiseMergeResult

	destination   string
	stage         string
	selectedStage string
	templateKind  string
}

// BuildMergePlan validates and prepares a merge without creating Destination.
// The returned plan owns temporary staging directories until ExecuteMergePlan.
func BuildMergePlan(request MergeRequest) (*MergePlan, error) {
	if request.Destination == "" {
		return nil, errors.New("merge destination is required")
	}
	selectedRoot, err := filepath.Abs(request.SelectedTemplateRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve selected template root: %w", err)
	}
	if request.SelectedTemplateRoot == "" {
		return nil, errors.New("selected template root is required")
	}
	if info, err := os.Stat(selectedRoot); err != nil {
		return nil, fmt.Errorf("inspect selected template root: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("selected template root %s is not a directory", selectedRoot)
	}

	sharedRoot := ""
	if request.SharedRegistryRoot != "" {
		sharedRoot, err = filepath.Abs(request.SharedRegistryRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve shared registry root: %w", err)
		}
		if info, err := os.Stat(sharedRoot); err != nil {
			return nil, fmt.Errorf("inspect shared registry root: %w", err)
		} else if !info.IsDir() {
			return nil, fmt.Errorf("shared registry root %s is not a directory", sharedRoot)
		}
	}
	destination, err := filepath.Abs(request.Destination)
	if err != nil {
		return nil, fmt.Errorf("resolve merge destination: %w", err)
	}
	if selectedRoot == destination || (sharedRoot != "" && sharedRoot == destination) {
		return nil, errors.New("merge source and destination must differ")
	}
	if _, err := os.Lstat(destination); err == nil {
		return nil, fmt.Errorf("destination %s already exists", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect merge destination: %w", err)
	}
	for _, source := range []string{selectedRoot, sharedRoot} {
		if source != "" && source != destination && inside(source, destination) {
			return nil, fmt.Errorf("merge destination %s is inside source %s", destination, source)
		}
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return nil, fmt.Errorf("create merge destination parent: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".premise-stage-*")
	if err != nil {
		return nil, fmt.Errorf("create merge staging directory: %w", err)
	}
	selectedStage, err := os.MkdirTemp("", "premise-selected-*")
	if err != nil {
		_ = os.RemoveAll(stage)
		return nil, fmt.Errorf("create selected template staging directory: %w", err)
	}
	plan := &MergePlan{
		destination: destination, stage: stage, selectedStage: selectedStage,
		templateKind: request.TemplateKind,
	}
	keepStages := false
	defer func() {
		if !keepStages {
			_ = plan.close()
		}
	}()

	if err = copyTree(selectedRoot, selectedStage, literalReplacer(request.Substitutions)); err != nil {
		return nil, fmt.Errorf("stage selected template: %w", err)
	}
	shared, err := readSharedValues(sharedRoot, literalReplacer(request.Substitutions))
	if err != nil {
		return nil, err
	}
	selected, rootMode, err := readTreeValues(selectedStage, nil)
	if err != nil {
		return nil, err
	}
	plan.Root = PathValue{Present: true, Kind: "dir", Mode: rootMode}
	resolver := request.ResolveConflict
	registryIdentity := request.RegistryIdentity
	if registryIdentity == "" {
		registryIdentity = request.SelectedIdentity
	}
	if registryIdentity == "" {
		registryIdentity = filepath.Base(selectedRoot)
	}
	paths := unionPathKeys(shared, selected)
	for _, path := range paths {
		sharedValue, sharedOK := shared[path]
		selectedValue, selectedOK := selected[path]
		sharedValue.Present = sharedOK
		selectedValue.Present = selectedOK
		entry := MergeEntry{Path: path, Shared: sharedValue, Selected: selectedValue}
		switch {
		case !sharedOK || !selectedOK:
			entry.Output = clonePathValue(selectPresent(sharedValue, selectedValue, sharedOK))
			entry.Strategy = MergeStrategyCopy
		case samePathValue(sharedValue, selectedValue):
			entry.Output = clonePathValue(selectedValue)
			entry.Strategy = MergeStrategyEqual
		default:
			conflict := MergeConflict{Path: path, Key: path, Kind: pathKind(sharedValue, selectedValue), Shared: describePathValue(sharedValue), Selected: describePathValue(selectedValue)}
			plan.Collisions = append(plan.Collisions, conflict)
			if path == "mise.toml" && sharedValue.Kind == "file" && selectedValue.Kind == "file" {
				if err = mergeTierOneMise(plan, &entry, sharedValue, selectedValue, request.TemplateKind, registryIdentity, resolver); err != nil {
					return nil, err
				}
			} else if isOrderedMergePath(path) && sharedValue.Kind == "file" && selectedValue.Kind == "file" {
				if err = mergeTierTwoLines(&entry, sharedValue, selectedValue, resolver); err != nil {
					return nil, err
				}
			} else {
				if err = mergeTierThreeWholeFile(&entry, conflict, resolver); err != nil {
					return nil, err
				}
			}
		}
		plan.Entries = append(plan.Entries, entry)
	}
	plan.Entries = pruneOutputFileDescendants(plan.Entries)
	keepStages = true
	return plan, nil
}

// ExecuteMergePlan validates a prepared merge and publishes it with a final
// same-filesystem rename; only that rename is atomic.
func ExecuteMergePlan(ctx context.Context, plan *MergePlan, output io.Writer) (err error) {
	if plan == nil || plan.stage == "" {
		return errors.New("merge plan is nil or already executed")
	}
	if output == nil {
		output = io.Discard
	}
	published := false
	defer func() {
		cleanupErr := plan.close()
		if cleanupErr == nil {
			return
		}
		if published {
			if rollbackErr := os.RemoveAll(plan.destination); rollbackErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("rollback merge destination: %w", rollbackErr))
			}
		}
		err = errors.Join(err, cleanupErr)
	}()

	if err = validateMergeToolChanges(ctx, plan.selectedStage, plan.templateKind, plan.Mise.ToolChanges, output); err != nil {
		return err
	}
	if err = materializeMergePlan(plan, plan.stage); err != nil {
		return err
	}
	if err = validateMergeCandidate(ctx, plan.stage, plan.templateKind, output); err != nil {
		return err
	}
	if _, statErr := os.Lstat(plan.destination); statErr == nil {
		return fmt.Errorf("destination %s already exists", plan.destination)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect merge destination: %w", statErr)
	}
	if err = os.Rename(plan.stage, plan.destination); err != nil {
		return fmt.Errorf("commit merge: %w", err)
	}
	plan.stage = ""
	published = true
	return nil
}

func (plan *MergePlan) close() error {
	var failures []error
	if plan.stage != "" {
		failures = append(failures, os.RemoveAll(plan.stage))
		plan.stage = ""
	}
	if plan.selectedStage != "" {
		failures = append(failures, os.RemoveAll(plan.selectedStage))
		plan.selectedStage = ""
	}
	return errors.Join(failures...)
}

func mergeTierOneMise(plan *MergePlan, entry *MergeEntry, shared, selected PathValue, kind, identity string, resolver MergeConflictResolver) error {
	result, err := composeMise(shared.Data, selected.Data, kind, identity, resolver)
	if err != nil {
		return fmt.Errorf("merge mise.toml: %w", err)
	}
	mode, err := resolveMergedMode(entry.Path, shared, selected, resolver)
	if err != nil {
		return err
	}
	entry.Output = PathValue{Present: true, Kind: "file", Mode: mode, Data: append([]byte(nil), result.Bytes...)}
	entry.Strategy = MergeStrategyMise
	plan.Mise = result
	return nil
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

func validateMergeToolChanges(ctx context.Context, selectedStage, kind string, changes []ToolChange, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	for _, change := range changes {
		candidate, err := os.MkdirTemp("", "premise-tool-preflight-*")
		if err != nil {
			return fmt.Errorf("create %s tool preflight: %w", change.Name, err)
		}
		failure := func() error {
			defer os.RemoveAll(candidate)
			if err := copyTree(selectedStage, candidate, nil); err != nil {
				return err
			}
			if err := updateMiseTool(filepath.Join(candidate, "mise.toml"), change.Name, change.To); err != nil {
				return err
			}
			label := fmt.Sprintf("tool %s %v -> %v", change.Name, change.From, change.To)
			if err := runTemplateContracts(ctx, candidate, kind, label, output, output); err != nil {
				return fmt.Errorf("tool update %s %v -> %v failed: %w", change.Name, change.From, change.To, err)
			}
			return nil
		}()
		if failure != nil {
			return failure
		}
	}
	return nil
}

func validateMergeCandidate(ctx context.Context, stage, kind string, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	candidate, err := os.MkdirTemp("", "premise-candidate-validation-*")
	if err != nil {
		return fmt.Errorf("create generated candidate validation copy: %w", err)
	}
	defer os.RemoveAll(candidate)
	if err := copyTree(stage, candidate, nil); err != nil {
		return fmt.Errorf("copy generated candidate: %w", err)
	}
	if err := runTemplateContracts(ctx, candidate, kind, "merged candidate", output, output); err != nil {
		return fmt.Errorf("generated candidate validation failed: %w", err)
	}
	return nil
}

func materializeMergePlan(plan *MergePlan, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("create merge stage: %w", err)
	}
	var directories []MergeEntry
	for _, entry := range plan.Entries {
		target := filepath.Join(destination, filepath.FromSlash(entry.Path))
		if !inside(destination, target) {
			return fmt.Errorf("merge entry escapes root: %s", target)
		}
		switch entry.Output.Kind {
		case "dir":
			if err := os.MkdirAll(target, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("create merge directory %s: %w", entry.Path, err)
			}
			directories = append(directories, entry)
		case "file":
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create merge parent %s: %w", entry.Path, err)
			}
			if err := os.WriteFile(target, entry.Output.Data, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("write merge file %s: %w", entry.Path, err)
			}
			if err := os.Chmod(target, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("preserve merge file permissions %s: %w", entry.Path, err)
			}
		default:
			return fmt.Errorf("unsupported output kind %q for %s", entry.Output.Kind, entry.Path)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.Count(directories[i].Path, "/") > strings.Count(directories[j].Path, "/")
	})
	for _, entry := range directories {
		if err := os.Chmod(filepath.Join(destination, filepath.FromSlash(entry.Path)), entry.Output.Mode.Perm()); err != nil {
			return fmt.Errorf("preserve merge directory permissions %s: %w", entry.Path, err)
		}
	}
	if plan.Root.Present {
		if err := os.Chmod(destination, plan.Root.Mode.Perm()); err != nil {
			return fmt.Errorf("preserve merge root permissions: %w", err)
		}
	}
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

func readSharedValues(root string, replacer *strings.Replacer) (map[string]PathValue, error) {
	values := make(map[string]PathValue)
	if root == "" {
		return values, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read shared registry root: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect shared entry %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("unsupported shared entry %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read shared file %s: %w", entry.Name(), err)
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
			return fmt.Errorf("source entry escapes merge root: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect merge entry %s: %w", path, err)
		}
		if relative == "." {
			if !info.IsDir() {
				return fmt.Errorf("merge root %s is not a directory", path)
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
			return fmt.Errorf("unsupported merge entry %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read merge file %s: %w", relative, err)
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
func selectPresent(left, right PathValue, leftOK bool) PathValue {
	if leftOK {
		return left
	}
	return right
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

func unionPathKeys(left, right map[string]PathValue) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for path := range left {
		seen[path] = struct{}{}
	}
	for path := range right {
		seen[path] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
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
