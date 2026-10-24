package core

import (
	"strings"
	"testing"
)

// Tests ----------------------------------------------------------------------

func TestMergeOrderedPolicyPreservesPrecedenceAndAdjacentDuplicates(t *testing.T) {
	shared := []byte("# shared\r\ncache/\r\ncache/\r\n!cache/keep\r\nrepeat\r\n")
	selected := []byte("repeat\n# selected\nrepeat\n")
	want := "# shared\ncache/\n!cache/keep\nrepeat\n# selected\nrepeat\n"
	if got := string(mergeOrderedLines(shared, selected)); got != want {
		t.Fatalf("mergeOrderedLines() = %q, want %q", got, want)
	}
}

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

func TestDependencyVersionMerging(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		shared    string
		selected  string
		choice    MergeChoice
		want      string
		wantError string
	}{
		{"when both versions match", "1.2.0", "1.2.0", MergeChoiceAbort, "1.2.0", ""},
		{"when the incoming minor version is newer", "1.3.0", "1.2.0", MergeChoiceAbort, "1.3.0", ""},
		{"when the client patch version is newer", "v1.2.4", "v1.2.5", MergeChoiceAbort, "v1.2.5", ""},
		{"when major versions differ", "2.0.0", "1.9.0", MergeChoiceKeepShared, "", "incompatible major versions"},
		{"when a range requires a client decision", "^1.2", "~1.3", MergeChoiceUseSelected, "~1.3", ""},
		{"when a selector requires an incoming decision", "lts", "latest", MergeChoiceKeepShared, "lts", ""},
		{"when an incomparable selector is rejected", "workspace:*", "file:../helper", MergeChoiceAbort, "", "merge aborted"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			conflict := MergeConflict{Path: "manifest", Key: "dependencies.tool", Kind: "dependency", Shared: scenario.shared, Selected: scenario.selected}
			resolver := MergeDecisions{conflict.Key: {Choice: scenario.choice}}

			// Act
			selected, err := MergeDependencyVersion(conflict, resolver.Resolve)

			// Assert
			t.Run("it preserves compatible versions and requires an explicit decision otherwise", func(t *testing.T) {
				if scenario.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), scenario.wantError) {
						t.Fatalf("error = %v, want %q", err, scenario.wantError)
					}
					return
				}
				if err != nil || selected != scenario.want {
					t.Fatalf("selected = %q, error = %v; want %q", selected, err, scenario.want)
				}
			})
		})
	}
}
