package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/huh/v2"
)

// Types ----------------------------------------------------------------------

// InteractiveQuestionnaire collects generation answers from a terminal.
type InteractiveQuestionnaire struct{}

var _ Questionnaire = InteractiveQuestionnaire{}

// Public API -----------------------------------------------------------------

func (InteractiveQuestionnaire) Ask(questions []Question) (map[string]string, error) {
	answers := make(map[string]string, len(questions))
	for _, question := range questions {
		var value string
		var err error
		switch question.Type {
		case "string":
			err = huh.NewInput().
				Title(question.Prompt).
				Value(&value).
				Validate(requiredAnswer).
				Run()
		case "radio", "select":
			err = huh.NewSelect[string]().
				Title(question.Prompt).
				Options(huh.NewOptions(question.Choices...)...).
				Value(&value).
				Run()
		default:
			return nil, fmt.Errorf("unsupported question type %q", question.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("answer %s: %w", question.Populate, err)
		}
		value = strings.TrimSpace(value)
		if err := requiredAnswer(value); err != nil {
			return nil, fmt.Errorf("answer %s: %w", question.Populate, err)
		}
		answers[question.Populate] = value
	}
	return answers, nil
}

func requiredAnswer(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("a value is required")
	}
	return nil
}

// AskTemplateKind lets a user choose a supported template kind.
func AskTemplateKind() (string, error) {
	var kind string
	err := huh.NewSelect[string]().
		Title("Template kind:").
		Options(huh.NewOptions("app", "lib")...).
		Value(&kind).
		Run()
	if err != nil {
		return "", fmt.Errorf("choose template kind: %w", err)
	}
	return kind, nil
}

// AskDefaultTemplate first selects an official registry, then a template from
// that registry. Progress is written to progress.
func AskDefaultTemplate(ctx context.Context, progress io.Writer) (string, error) {
	if progress == nil {
		progress = io.Discard
	}
	source, err := promptPickRegistry()
	if err != nil {
		return "", fmt.Errorf("choose template registry: %w", err)
	}
	fmt.Fprintf(progress, "Selected template registry %s\n", source)
	return AskRegistryTemplate(ctx, source, progress)
}

var promptPickRegistry = func() (string, error) {
	options := make([]huh.Option[string], len(OfficialSources))
	for index, source := range OfficialSources {
		options[index] = huh.NewOption(registryLabel(source), source)
	}
	var source string
	err := huh.NewSelect[string]().
		Title("Template registry:").
		Options(options...).
		Value(&source).
		Run()
	if err != nil {
		return "", fmt.Errorf("choose template registry: %w", err)
	}
	return source, nil
}

func registryLabel(source string) string {
	switch source {
	case NativeTemplateSource:
		return "Default (Go)"
	case "cloudvoyant/premise-cargo":
		return "Rust"
	case "cloudvoyant/premise-bun":
		return "TypeScript"
	default:
		return source
	}
}

// AskRegistryTemplate lets a user choose a template from one registry source,
// reports loading progress to progress, and returns the selected selector.
func AskRegistryTemplate(ctx context.Context, source string, progress io.Writer) (string, error) {
	if progress == nil {
		progress = io.Discard
	}
	fmt.Fprintf(progress, "Loading template registry %s...\n", source)
	started := time.Now()
	entries, err := loadSourceEntries(ctx, source)
	if err != nil {
		return "", fmt.Errorf("load template registry %s: %w", source, err)
	}
	fmt.Fprintf(progress, "Loaded %s (%d templates, %s)\n", source, len(entries), time.Since(started).Round(time.Millisecond))
	if len(entries) == 0 {
		return "", fmt.Errorf("registry %s declares no templates", source)
	}
	selector, err := promptPickEntry(entries)
	if err != nil {
		return "", fmt.Errorf("choose template from %s: %w", source, err)
	}
	return selector, nil
}

// Private helpers ------------------------------------------------------------

// promptPickEntry opens an interactive picker over the given entries and returns
// the selected entry's selector. It is a package variable so tests can
// substitute a deterministic selection.
var promptPickEntry = func(entries []RegistryEntry) (string, error) {
	options := make([]huh.Option[string], len(entries))
	for index, entry := range entries {
		options[index] = huh.NewOption(entry.Label, entry.Selector())
	}
	var selector string
	err := huh.NewSelect[string]().
		Title("Template:").
		Options(options...).
		Value(&selector).
		Run()
	if err != nil {
		return "", fmt.Errorf("choose template: %w", err)
	}
	return selector, nil
}
