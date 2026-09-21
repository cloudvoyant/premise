package core

import (
	"bytes"
	"fmt"
	"io/fs"
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
