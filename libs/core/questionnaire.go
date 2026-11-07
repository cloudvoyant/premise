package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/huh/v2"
)

// Types ----------------------------------------------------------------------

// InteractiveQuestionnaire collects generation answers from a terminal.
type InteractiveQuestionnaire struct{}

var _ Questionnaire = InteractiveQuestionnaire{}

// Public API -----------------------------------------------------------------

// AskInfrastructureSetup collects the optional workspace infrastructure choices.
func AskInfrastructureSetup(root string) (bool, InfrastructureSetup, error) {
	var enabled bool
	var setup InfrastructureSetup
	if err := huh.NewConfirm().Title("Does this workspace need infrastructure?").Value(&enabled).Run(); err != nil || !enabled {
		return enabled, setup, err
	}
	_, exists, err := existingPulumiLanguage(filepath.Join(root, "infra", "Pulumi.yaml"))
	if err != nil {
		return false, setup, err
	}
	if !exists {
		if err := huh.NewSelect[string]().Title("Pulumi language:").Options(huh.NewOptions("go", "typescript", "python", "javascript", "yaml", "csharp", "java")...).Value(&setup.Language).Run(); err != nil {
			return false, setup, err
		}
	}
	if err := huh.NewInput().Title("Pulumi version (leave empty to keep the existing pin or tested default):").Value(&setup.PulumiVersion).Run(); err != nil {
		return false, setup, err
	}
	return true, setup, nil
}

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

// AnswerQuestionnaire validates explicit answers for unattended generation.
type AnswerQuestionnaire struct{ Answers map[string]string }

// Ask requires every declared answer and rejects unknown keys or choices.
func (q AnswerQuestionnaire) Ask(questions []Question) (map[string]string, error) {
	result := map[string]string{}
	known := map[string]bool{}
	for _, question := range questions {
		known[question.Populate] = true
		value := strings.TrimSpace(q.Answers[question.Populate])
		if value == "" {
			return nil, fmt.Errorf("answer %s is required", question.Populate)
		}
		if question.Type != "string" && !slices.Contains(question.Choices, value) {
			return nil, fmt.Errorf("answer %s must be one of the declared choices", question.Populate)
		}
		result[question.Populate] = value
	}
	for key := range q.Answers {
		if !known[key] {
			return nil, fmt.Errorf("unknown answer %s", key)
		}
	}
	return result, nil
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

func requiredAnswer(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("a value is required")
	}
	return nil
}

var promptPickRegistry = func() (string, error) {
	sources := registryPickerSources()
	options := make([]huh.Option[string], len(sources))
	for index, source := range sources {
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

func registryPickerSources() []string {
	sources := make([]string, 0, len(OfficialSources))
	for _, source := range OfficialSources {
		if source != NativeTemplateSource {
			sources = append(sources, source)
		}
	}
	return append(sources, NativeTemplateSource)
}

func registryLabel(source string) string {
	switch source {
	case NativeTemplateSource:
		return "Default (language-agnostic)"
	case "cloudvoyant/premise-cargo":
		return "Rust"
	case "cloudvoyant/premise-bun":
		return "TypeScript"
	default:
		return source
	}
}
