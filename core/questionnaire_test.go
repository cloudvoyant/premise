package core

import "testing"

// Public API -----------------------------------------------------------------

func TestInteractiveQuestionnaireRemainsAQuestionnaire(t *testing.T) {
	var questionnaire Questionnaire = InteractiveQuestionnaire{}
	if questionnaire == nil {
		t.Fatal("interactive questionnaire is nil")
	}
}

func TestUnattendedGenerationAnswers(t *testing.T) {
	questions := []Question{{Populate: "name", Type: "string"}, {Populate: "mode", Type: "select", Choices: []string{"private"}}}
	for _, answers := range []AnswerQuestionnaire{{"name": "site"}, {"name": "site", "mode": "public"}, {"name": "site", "mode": "private", "extra": "oops"}} {
		if _, err := answers.Ask(questions); err == nil {
			t.Fatalf("accepted invalid answers: %v", answers)
		}
	}
	if _, err := (AnswerQuestionnaire{"name": "site", "mode": "private"}).Ask(questions); err != nil {
		t.Fatal(err)
	}
}
