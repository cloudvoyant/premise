package core

import (
	"context"
	"errors"
	"fmt"
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

// AskDefaultTemplate lets a user choose a template from the default registry.
func AskDefaultTemplate(ctx context.Context) (string, error) {
	registry, err := DefaultRegistry(ctx)
	if err != nil {
		return "", err
	}
	names := registry.Names()
	if len(names) == 0 {
		return "", errors.New("default template registry is empty")
	}
	var name string
	err = huh.NewSelect[string]().
		Title("Template:").
		Options(huh.NewOptions(names...)...).
		Value(&name).
		Run()
	if err != nil {
		return "", fmt.Errorf("choose template: %w", err)
	}
	return ":" + name, nil
}
