package core

import (
	"testing"
)

// -----------------------------------------------------------------------------
// File-level merge policy
// -----------------------------------------------------------------------------

func TestMergeOrderedPolicyPreservesPrecedenceAndAdjacentDuplicates(t *testing.T) {
	shared := []byte("# shared\r\ncache/\r\ncache/\r\n!cache/keep\r\nrepeat\r\n")
	selected := []byte("repeat\n# selected\nrepeat\n")
	want := "# shared\ncache/\n!cache/keep\nrepeat\n# selected\nrepeat\n"
	if got := string(mergeOrderedLines(shared, selected)); got != want {
		t.Fatalf("mergeOrderedLines() = %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// Deterministic conflict resolution
// -----------------------------------------------------------------------------

func TestMergeDecisionsResolveByKeyPathAndAbort(t *testing.T) {
	decisions := MergeDecisions{
		"key":  {Choice: MergeChoiceKeepShared},
		"path": {Choice: MergeChoiceUseSelected},
	}
	for _, test := range []struct {
		name     string
		conflict MergeConflict
		want     MergeChoice
	}{
		{name: "key", conflict: MergeConflict{Key: "key", Path: "other"}, want: MergeChoiceKeepShared},
		{name: "path", conflict: MergeConflict{Key: "missing", Path: "path"}, want: MergeChoiceUseSelected},
		{name: "default", conflict: MergeConflict{Key: "missing", Path: "other"}, want: MergeChoiceAbort},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := decisions.Resolve(test.conflict)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Choice != test.want {
				t.Fatalf("choice = %q, want %q", decision.Choice, test.want)
			}
		})
	}
}
