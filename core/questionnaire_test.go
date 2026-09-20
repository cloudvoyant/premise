package core

import "testing"

func TestInteractiveQuestionnaireRemainsAQuestionnaire(t *testing.T) {
	var questionnaire Questionnaire = InteractiveQuestionnaire{}
	if questionnaire == nil {
		t.Fatal("interactive questionnaire is nil")
	}
}
