package core

import (
	"fmt"
	"slices"
	"strings"
)

// AnswerQuestionnaire validates explicit answers for unattended generation.
type AnswerQuestionnaire struct{ Answers map[string]string }

// Public API -----------------------------------------------------------------

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
