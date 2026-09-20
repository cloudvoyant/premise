package core

import (
	"errors"
	"strings"
	"testing"
)

func TestInteractiveQuestionnaireResolvesMergeChoices(t *testing.T) {
	originalChoice := promptMergeChoice
	originalRename := promptMergeRename
	t.Cleanup(func() {
		promptMergeChoice = originalChoice
		promptMergeRename = originalRename
	})

	conflict := MergeConflict{Path: "mise.toml", Key: "DATABASE", Kind: "environment", AllowRename: true}
	promptMergeChoice = func(got MergeConflict) (MergeChoice, error) {
		if got.Key != conflict.Key {
			t.Fatalf("conflict key = %q, want %q", got.Key, conflict.Key)
		}
		return MergeChoiceRenameSelected, nil
	}
	promptMergeRename = func(got MergeConflict) (string, error) {
		if !got.AllowRename {
			t.Fatal("rename prompt received a non-renamable conflict")
		}
		return "SELECTED_DATABASE", nil
	}

	decision, err := (InteractiveQuestionnaire{}).ResolveMergeConflict(conflict)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Choice != MergeChoiceRenameSelected || decision.Rename != "SELECTED_DATABASE" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestInteractiveQuestionnaireSkipsRenamePromptForWholeFileChoice(t *testing.T) {
	originalChoice := promptMergeChoice
	originalRename := promptMergeRename
	t.Cleanup(func() {
		promptMergeChoice = originalChoice
		promptMergeRename = originalRename
	})

	promptMergeChoice = func(MergeConflict) (MergeChoice, error) {
		return MergeChoiceUseSelected, nil
	}
	promptMergeRename = func(MergeConflict) (string, error) {
		t.Fatal("rename prompt should not run")
		return "", nil
	}

	decision, err := (InteractiveQuestionnaire{}).ResolveMergeConflict(MergeConflict{Path: "NOTICE", Key: "NOTICE", Kind: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if decision != (MergeDecision{Choice: MergeChoiceUseSelected}) {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestInteractiveQuestionnaireWrapsMergePromptErrors(t *testing.T) {
	originalChoice := promptMergeChoice
	t.Cleanup(func() { promptMergeChoice = originalChoice })
	promptMergeChoice = func(MergeConflict) (MergeChoice, error) {
		return "", errors.New("terminal closed")
	}

	_, err := (InteractiveQuestionnaire{}).ResolveMergeConflict(MergeConflict{Key: "tools.node"})
	if err == nil || !strings.Contains(err.Error(), "resolve merge conflict tools.node: terminal closed") {
		t.Fatalf("error = %v", err)
	}
}
