package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Selection is a parsed template selector. Source is the registry identity,
// local path, or URL the template lives in; Name is the template name; Local
// reports whether Source refers to a filesystem path rather than a registry.
type Selection struct {
	Source string
	Name   string
	Local  bool
}

// ParseSelector splits a template selector into its source and template name.
// Selectors must end with :<template>; a bare :<template> shorthand resolves
// against the native registry. Sources of ".", "./", "../", or an absolute path
// are treated as local template roots.
func ParseSelector(selector string) (Selection, error) {
	separator := strings.LastIndex(selector, ":")
	if separator < 0 || separator == len(selector)-1 {
		return Selection{}, fmt.Errorf("template selector %q must end with :<template>", selector)
	}
	source := selector[:separator]
	name := selector[separator+1:]
	if err := ValidateTemplateName(name); err != nil {
		return Selection{}, err
	}
	if source == "" {
		source = NativeTemplateSource
	}
	local := source == "." || filepath.IsAbs(source) || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
	return Selection{Source: source, Name: name, Local: local}, nil
}

// SelectorKind classifies a generate argument into the selector resolution
// strategy the command dispatches.
type SelectorKind int

const (
	// SelectorExplicit marks a fully qualified selector — an explicit
	// <owner>/<repo>:<template>, local path, or URL — that passes through to
	// generation unchanged.
	SelectorExplicit SelectorKind = iota
	// SelectorSource marks a colon-free <owner>/<repo> source that loads that
	// one registry for a picker scoped to its templates.
	SelectorSource
	// SelectorOfficialName marks a bare :<template> shorthand resolved against
	// every official registry.
	SelectorOfficialName
	// SelectorDefault marks the empty argument, which opens the default
	// interactive picker over every official registry.
	SelectorDefault
)

// ClassifiedSelector is the parsed, classified form of a generate argument,
// carrying the resolution strategy to dispatch and the payload that strategy
// consumes.
type ClassifiedSelector struct {
	Kind  SelectorKind
	Value string
}

// ClassifyGenerateArgument parses a generate command argument into the selector
// resolution strategy the caller should dispatch. The empty argument selects
// the default picker. A leading-colon shorthand names a template without a
// source and resolves against every official registry. A colon-free argument
// is a source-only <owner>/<repo> that loads that one registry for a scoped
// picker. Any other argument must be a fully qualified
// <owner>/<repo>:<template>, local, or URL selector; a colon-carrying argument
// that fails to parse is malformed and returns the validation error instead of
// being treated as a cloneable source.
func ClassifyGenerateArgument(arg string) (ClassifiedSelector, error) {
	if arg == "" {
		return ClassifiedSelector{Kind: SelectorDefault}, nil
	}
	if name, ok := strings.CutPrefix(arg, ":"); ok {
		if err := ValidateTemplateName(name); err != nil {
			return ClassifiedSelector{}, err
		}
		return ClassifiedSelector{Kind: SelectorOfficialName, Value: name}, nil
	}
	if !strings.Contains(arg, ":") {
		return ClassifiedSelector{Kind: SelectorSource, Value: arg}, nil
	}
	if _, err := ParseSelector(arg); err != nil {
		return ClassifiedSelector{}, err
	}
	return ClassifiedSelector{Kind: SelectorExplicit, Value: arg}, nil
}
