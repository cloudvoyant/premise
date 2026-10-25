package core

import (
	"encoding/json"
	"reflect"
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

func TestMergeJSON(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		shared    string
		client    string
		want      string
		choice    MergeChoice
		wantError string
	}{
		{
			name: "when nested fields are disjoint or equal",
			shared: `{
  "settings": {"shared": true, "equal": "same"},
  "items": ["shared", "common"]
}`,
			client: `{
  "settings": {"client": true, "equal": "same"},
  "items": ["common", "client"]
}`,
			want: `{
  "settings": {"shared": true, "client": true, "equal": "same"},
  "items": ["shared", "common", "client"]
}`,
		},
		{
			name:   "when the caller chooses the shared scalar",
			shared: `{"settings": {"version": "2.0.0"}}`,
			client: `{"settings": {"version": "1.0.0"}}`,
			want:   `{"settings": {"version": "2.0.0"}}`,
			choice: MergeChoiceKeepShared,
		},
		{
			name:   "when the caller chooses the client scalar",
			shared: `{"settings": {"version": "2.0.0"}}`,
			client: `{"settings": {"version": "1.0.0"}}`,
			want:   `{"settings": {"version": "1.0.0"}}`,
			choice: MergeChoiceUseSelected,
		},
		{
			name:      "when the caller aborts a scalar conflict",
			shared:    `{"settings": {"version": "2.0.0"}}`,
			client:    `{"settings": {"version": "1.0.0"}}`,
			choice:    MergeChoiceAbort,
			wantError: "merge aborted for manifest value settings.version",
		},
		{
			name: "when the shared document is empty",
			client: `{
  "settings": {"client": true, "equal": "same"},
  "items": ["common", "client"]
}`,
			want: `{
  "settings": {"client": true, "equal": "same"},
  "items": ["common", "client"]
}`,
		},
		{
			name: "when the client document is empty",
			shared: `{
  "settings": {"shared": true, "equal": "same"},
  "items": ["shared", "common"]
}`,
			want: `{
  "settings": {"shared": true, "equal": "same"},
  "items": ["shared", "common"]
}`,
		},
		{
			name:      "when the shared document is malformed",
			shared:    `{"unfinished":`,
			wantError: "parse shared settings",
		},
		{
			name:      "when the client contains null",
			client:    `null`,
			wantError: "parse client settings",
		},
		{
			name:      "when the client contains an array",
			client:    `[]`,
			wantError: "parse client settings",
		},
		{
			name:      "when the client contains multiple objects",
			client:    `{} {}`,
			wantError: "parse client settings",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			shared, client := []byte(scenario.shared), []byte(scenario.client)
			decisions := MergeDecisions{"settings.version": {Choice: scenario.choice}}

			// Act
			merged, err := MergeJSON("settings", shared, client, decisions.Resolve)

			// Assert
			t.Run("it produces the expected JSON document or reports the conflict", func(t *testing.T) {
				if scenario.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), scenario.wantError) {
						t.Fatalf("error = %v, want %q", err, scenario.wantError)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var result, expected map[string]any
				if err := json.Unmarshal(merged, &result); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(scenario.want), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result, expected) {
					t.Fatalf("merged document:\n%s\nwant:\n%s", merged, scenario.want)
				}
			})
		})
	}
	t.Run("when the document contains an integer beyond float precision", func(t *testing.T) {
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
}

func TestMergeTOML(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		shared    string
		client    string
		want      string
		choice    MergeChoice
		wantError string
	}{
		{
			name: "when nested fields are disjoint or equal",
			shared: `items = ["shared", "common"]

[settings]
shared = true
equal = "same"
`,
			client: `items = ["common", "client"]

[settings]
client = true
equal = "same"
`,
			want: `items = ["shared", "common", "client"]

[settings]
shared = true
client = true
equal = "same"
`,
		},
		{
			name: "when the caller chooses the shared scalar",
			shared: `[settings]
version = "2.0.0"
`,
			client: `[settings]
version = "1.0.0"
`,
			want: `[settings]
version = "2.0.0"
`,
			choice: MergeChoiceKeepShared,
		},
		{
			name: "when the caller chooses the client scalar",
			shared: `[settings]
version = "2.0.0"
`,
			client: `[settings]
version = "1.0.0"
`,
			want: `[settings]
version = "1.0.0"
`,
			choice: MergeChoiceUseSelected,
		},
		{
			name: "when the caller aborts a scalar conflict",
			shared: `[settings]
version = "2.0.0"
`,
			client: `[settings]
version = "1.0.0"
`,
			choice:    MergeChoiceAbort,
			wantError: "merge aborted for manifest value settings.version",
		},
		{
			name: "when the shared document is empty",
			client: `items = ["common", "client"]

[settings]
client = true
equal = "same"
`,
			want: `items = ["common", "client"]

[settings]
client = true
equal = "same"
`,
		},
		{
			name: "when the client document is empty",
			shared: `items = ["shared", "common"]

[settings]
shared = true
equal = "same"
`,
			want: `items = ["shared", "common"]

[settings]
shared = true
equal = "same"
`,
		},
		{
			name:      "when the shared document is malformed",
			shared:    `unfinished = [`,
			wantError: "parse shared settings",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			shared, client := []byte(scenario.shared), []byte(scenario.client)
			decisions := MergeDecisions{"settings.version": {Choice: scenario.choice}}

			// Act
			merged, err := MergeTOML("settings", shared, client, decisions.Resolve)

			// Assert
			t.Run("it produces the expected TOML document or reports the conflict", func(t *testing.T) {
				if scenario.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), scenario.wantError) {
						t.Fatalf("error = %v, want %q", err, scenario.wantError)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var result, expected map[string]any
				if err := toml.Unmarshal(merged, &result); err != nil {
					t.Fatal(err)
				}
				if err := toml.Unmarshal([]byte(scenario.want), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result, expected) {
					t.Fatalf("merged document:\n%s\nwant:\n%s", merged, scenario.want)
				}
			})
		})
	}
}

func TestMergeYAML(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		shared    string
		client    string
		want      string
		choice    MergeChoice
		wantError string
	}{
		{
			name: "when nested fields are disjoint or equal",
			shared: `settings:
  shared: true
  equal: same
items: [shared, common]
`,
			client: `settings:
  client: true
  equal: same
items: [common, client]
`,
			want: `settings:
  shared: true
  client: true
  equal: same
items: [shared, common, client]
`,
		},
		{
			name: "when the caller chooses the shared scalar",
			shared: `settings:
  version: "2.0.0"
`,
			client: `settings:
  version: "1.0.0"
`,
			want: `settings:
  version: "2.0.0"
`,
			choice: MergeChoiceKeepShared,
		},
		{
			name: "when the caller chooses the client scalar",
			shared: `settings:
  version: "2.0.0"
`,
			client: `settings:
  version: "1.0.0"
`,
			want: `settings:
  version: "1.0.0"
`,
			choice: MergeChoiceUseSelected,
		},
		{
			name: "when the caller aborts a scalar conflict",
			shared: `settings:
  version: "2.0.0"
`,
			client: `settings:
  version: "1.0.0"
`,
			choice:    MergeChoiceAbort,
			wantError: "merge aborted for manifest value settings.version",
		},
		{
			name: "when the shared document is empty",
			client: `settings:
  client: true
  equal: same
items: [common, client]
`,
			want: `settings:
  client: true
  equal: same
items: [common, client]
`,
		},
		{
			name: "when the client document is empty",
			shared: `settings:
  shared: true
  equal: same
items: [shared, common]
`,
			want: `settings:
  shared: true
  equal: same
items: [shared, common]
`,
		},
		{
			name:      "when the shared document is malformed",
			shared:    `unfinished: [`,
			wantError: "parse shared settings",
		},
		{
			name:      "when the client contains null",
			client:    `null`,
			wantError: "parse client settings",
		},
		{
			name:      "when the client contains an array",
			client:    `[]`,
			wantError: "parse client settings",
		},
		{
			name: "when the client contains multiple documents",
			client: `a: b
---
c: d
`,
			wantError: "parse client settings",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			shared, client := []byte(scenario.shared), []byte(scenario.client)
			decisions := MergeDecisions{"settings.version": {Choice: scenario.choice}}

			// Act
			merged, err := MergeYAML("settings", shared, client, decisions.Resolve)

			// Assert
			t.Run("it produces the expected YAML document or reports the conflict", func(t *testing.T) {
				if scenario.wantError != "" {
					if err == nil || !strings.Contains(err.Error(), scenario.wantError) {
						t.Fatalf("error = %v, want %q", err, scenario.wantError)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var result, expected map[string]any
				if err := yaml.Unmarshal(merged, &result); err != nil {
					t.Fatal(err)
				}
				if err := yaml.Unmarshal([]byte(scenario.want), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result, expected) {
					t.Fatalf("merged document:\n%s\nwant:\n%s", merged, scenario.want)
				}
			})
		})
	}
}
