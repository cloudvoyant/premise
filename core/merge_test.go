package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
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
			merged, err := MergeDependencies("manifest", []Dependency{{Name: "tool", Scope: []string{"dependencies"}, Version: scenario.shared}}, []Dependency{{Name: "tool", Scope: []string{"dependencies"}, Version: scenario.selected}}, resolver.Resolve)
			selected := ""
			if len(merged) > 0 {
				selected = merged[0].Version
			}

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

func TestStructuredDocumentMerging(t *testing.T) {
	for _, format := range []struct {
		name    string
		merge   func(string, []byte, []byte, MergeConflictResolver) ([]byte, error)
		encode  func(any) ([]byte, error)
		decode  func([]byte, any) error
		invalid string
	}{
		{
			name:    "JSON",
			merge:   MergeJSON,
			encode:  json.Marshal,
			decode:  json.Unmarshal,
			invalid: `{"unfinished":`,
		},
		{
			name:    "TOML",
			merge:   MergeTOML,
			encode:  toml.Marshal,
			decode:  toml.Unmarshal,
			invalid: "unfinished = [",
		},
		{
			name:    "YAML",
			merge:   MergeYAML,
			encode:  yaml.Marshal,
			decode:  yaml.Unmarshal,
			invalid: "unfinished: [",
		},
	} {
		t.Run("when merging "+format.name+" settings", func(t *testing.T) {
			t.Run("when fields are disjoint or equal", func(t *testing.T) {
				// Arrange
				shared, err := format.encode(map[string]any{
					"settings": map[string]any{
						"shared": true,
						"equal":  "same",
					},
					"items": []string{"shared", "common"},
				})
				if err != nil {
					t.Fatal(err)
				}
				client, err := format.encode(map[string]any{
					"settings": map[string]any{
						"client": true,
						"equal":  "same",
					},
					"items": []string{"common", "client"},
				})
				if err != nil {
					t.Fatal(err)
				}

				// Act
				merged, err := format.merge("settings", shared, client, MergeDecisions{}.Resolve)

				// Assert
				t.Run("it combines nested fields and distinct array items without a decision", func(t *testing.T) {
					if err != nil {
						t.Fatal(err)
					}
					var result struct {
						Settings map[string]any `json:"settings" toml:"settings" yaml:"settings"`
						Items    []string       `json:"items" toml:"items" yaml:"items"`
					}
					if err := format.decode(merged, &result); err != nil {
						t.Fatal(err)
					}
					if result.Settings["shared"] != true || result.Settings["client"] != true || result.Settings["equal"] != "same" || strings.Join(result.Items, ",") != "shared,common,client" {
						t.Fatalf("merged settings = %#v", result)
					}
				})
			})
			for _, scenario := range []struct {
				name   string
				choice MergeChoice
				want   string
			}{
				{
					name:   "when the caller chooses the shared scalar",
					choice: MergeChoiceKeepShared,
					want:   "2.0.0",
				},
				{
					name:   "when the caller chooses the client scalar",
					choice: MergeChoiceUseSelected,
					want:   "1.0.0",
				},
				{
					name:   "when the caller aborts a scalar conflict",
					choice: MergeChoiceAbort,
				},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					// Arrange
					shared, err := format.encode(map[string]any{"settings": map[string]any{"version": "2.0.0"}})
					if err != nil {
						t.Fatal(err)
					}
					client, err := format.encode(map[string]any{"settings": map[string]any{"version": "1.0.0"}})
					if err != nil {
						t.Fatal(err)
					}

					// Act
					merged, err := format.merge("settings", shared, client, MergeDecisions{"settings.version": {Choice: scenario.choice}}.Resolve)

					// Assert
					t.Run("it applies the field decision without interpreting a setting as a dependency", func(t *testing.T) {
						if scenario.choice == MergeChoiceAbort {
							if err == nil || !strings.Contains(err.Error(), "settings.version") {
								t.Fatalf("expected focused conflict, got %v", err)
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						var result struct {
							Settings map[string]string `json:"settings" toml:"settings" yaml:"settings"`
						}
						if err := format.decode(merged, &result); err != nil {
							t.Fatal(err)
						}
						if result.Settings["version"] != scenario.want {
							t.Fatalf("settings = %v, want %s", result.Settings, scenario.want)
						}
					})
				})
			}
			t.Run("when a document is malformed", func(t *testing.T) {
				// Arrange
				invalid := []byte(format.invalid)

				// Act
				_, err := format.merge("settings", invalid, nil, MergeDecisions{}.Resolve)

				// Assert
				t.Run("it reports which input could not be parsed", func(t *testing.T) {
					if err == nil || !strings.Contains(err.Error(), "parse shared settings") {
						t.Fatalf("expected input parse error, got %v", err)
					}
				})
			})
		})
	}
	t.Run("when JSON contains an integer beyond float precision", func(t *testing.T) {
		// Arrange
		shared := []byte(`{"integer":9007199254740993}`)

		// Act
		merged, err := MergeJSON("settings.json", shared, nil, MergeDecisions{}.Resolve)

		// Assert
		t.Run("it preserves the exact integer", func(t *testing.T) {
			if err != nil || !strings.Contains(string(merged), "9007199254740993") {
				t.Fatalf("integer changed: %s %v", merged, err)
			}
		})
	})
	for _, scenario := range []struct {
		name  string
		merge func(string, []byte, []byte, MergeConflictResolver) ([]byte, error)
		input string
	}{
		{
			name:  "when JSON contains null",
			merge: MergeJSON,
			input: "null",
		},
		{
			name:  "when JSON contains an array",
			merge: MergeJSON,
			input: "[]",
		},
		{
			name:  "when JSON has multiple objects",
			merge: MergeJSON,
			input: "{} {}",
		},
		{
			name:  "when YAML contains null",
			merge: MergeYAML,
			input: "null",
		},
		{
			name:  "when YAML contains an array",
			merge: MergeYAML,
			input: "[]",
		},
		{
			name:  "when YAML has multiple documents",
			merge: MergeYAML,
			input: "a: b\n---\nc: d\n",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			input := []byte(scenario.input)

			// Act
			_, err := scenario.merge("settings", nil, input, MergeDecisions{}.Resolve)

			// Assert
			t.Run("it rejects input that is not one object document", func(t *testing.T) {
				if err == nil || !strings.Contains(err.Error(), "parse client settings") {
					t.Fatalf("expected client parse error, got %v", err)
				}
			})
		})
	}
}
