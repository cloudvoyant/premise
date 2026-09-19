package core

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"

	"charm.land/huh/v2"
)

// InteractiveQuestionnaire collects generation answers from a terminal.
type InteractiveQuestionnaire struct{}

var _ Questionnaire = InteractiveQuestionnaire{}

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

// shuffleRegistryEntries randomizes the combined official template picker so
// no registry or language receives a stable first position.
var shuffleRegistryEntries = func(entries []RegistryEntry) {
	rand.Shuffle(len(entries), func(i, j int) {
		entries[i], entries[j] = entries[j], entries[i]
	})
}

// AskDefaultTemplate lets a user choose a template from the default registry
// and returns the selected entry's fully qualified selector.
func AskDefaultTemplate(ctx context.Context) (string, error) {
	entries, err := DefaultRegistry(ctx)
	if err != nil {
		return "", fmt.Errorf("load default template registry: %w", err)
	}
	if len(entries) == 0 {
		return "", errors.New("default template registry is empty")
	}
	shuffleRegistryEntries(entries)
	selector, err := promptPickEntry(entries)
	if err != nil {
		return "", fmt.Errorf("choose default template: %w", err)
	}
	return selector, nil
}

// AskRegistryTemplate lets a user choose a template from a single registry
// source. It loads that one registry and opens a picker scoped to its
// templates, returning the selected entry's fully qualified selector.
func AskRegistryTemplate(ctx context.Context, source string) (string, error) {
	entries, err := loadSourceEntries(ctx, source)
	if err != nil {
		return "", fmt.Errorf("load template registry %s: %w", source, err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("registry %s declares no templates", source)
	}
	selector, err := promptPickEntry(entries)
	if err != nil {
		return "", fmt.Errorf("choose template from %s: %w", source, err)
	}
	return selector, nil
}

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
