package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInteractiveQuestionnaireRemainsAQuestionnaire(t *testing.T) {
	var questionnaire Questionnaire = InteractiveQuestionnaire{}
	if questionnaire == nil {
		t.Fatal("interactive questionnaire is nil")
	}
}

// Public API -----------------------------------------------------------------

func TestExplicitAnswersRejectMissingUnknownAndInvalidChoices(t *testing.T) {
	questions := []Question{{Populate: "name", Type: "string"}, {Populate: "region", Type: "select", Choices: []string{"us", "eu"}}}
	for _, answers := range []map[string]string{{"name": "app"}, {"name": "app", "region": "invalid"}, {"name": "app", "region": "us", "extra": "unknown"}} {
		if _, err := (AnswerQuestionnaire{Answers: answers}).Ask(questions); err == nil {
			t.Fatal("accepted invalid answers")
		}
	}
	if _, err := (AnswerQuestionnaire{Answers: map[string]string{"name": "app", "region": "eu"}}).Ask(questions); err != nil {
		t.Fatal(err)
	}
}
func TestScaffoldPreservesSourceAndOmitsGeneratedSecrets(t *testing.T) {
	for _, name := range []string{"Pulumi.yaml", ".env.example", "package-lock.json", "database.ts"} {
		if generatedScaffoldEntry(name) {
			t.Fatalf("source %s excluded", name)
		}
	}
	for _, name := range []string{"Pulumi.preview-test.yaml", ".env", ".env.local", ".pulumi", "node_modules", "dist"} {
		if !generatedScaffoldEntry(name) {
			t.Fatalf("generated or secret %s included", name)
		}
	}
}

func TestOnlyDeclaredGeneratedPulumiSDKIsExcluded(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Pulumi.yaml"), []byte("name: example\npackages:\n  neon:\n    source: terraform-provider\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !generatedPulumiSDKDirectory(root, filepath.Join(root, "sdks/neon")) {
		t.Fatal("declared generated SDK copied")
	}
	if generatedPulumiSDKDirectory(root, filepath.Join(root, "sdks/client-owned")) {
		t.Fatal("ordinary SDK source excluded")
	}
}
