package core

import (
	"reflect"
	"strings"
	"testing"
)

func TestMergeMiseConfigsAppliesTypedRulesAndTracksSelectedUpdates(t *testing.T) {
	shared, err := ExtractMiseConfig("shared", []byte(`[tools]
rust = "1.82"
bun = "1.2"

[tasks.build]
run = ["echo shared", "echo duplicate"]

[env]
DATABASE = "shared"

[settings]
values = ["a", "b"]
color = "blue"
`))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ExtractMiseConfig("selected", []byte(`[tools]
rust = "1.83"
bun = "1.1"

[tasks.build]
run = ["echo duplicate", "echo $DATABASE env.DATABASE ${DATABASE} DATABASE_SUFFIX"]

[env]
DATABASE = "selected"

[settings]
values = ["b", "c"]
color = "green"
`))
	if err != nil {
		t.Fatal(err)
	}
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{
		"DATABASE":       {Choice: MergeChoiceRenameSelected, Rename: "SELECTED_DATABASE"},
		"settings.color": {Choice: MergeChoiceUseSelected},
	}}
	result, err := MergeMiseConfigs(shared, selected, "selected", resolver)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Config.Tools["rust"].Selector; got != "1.83" {
		t.Fatalf("rust = %q, want 1.83", got)
	}
	if got := result.Config.Tools["bun"].Selector; got != "1.2" {
		t.Fatalf("bun = %q, want 1.2", got)
	}
	wantChanges := []ToolChange{{Name: "bun", From: "1.1", To: "1.2"}}
	if !reflect.DeepEqual(result.ToolChanges, wantChanges) {
		t.Fatalf("tool changes = %#v, want %#v", result.ToolChanges, wantChanges)
	}
	wantRun := []string{
		"echo shared",
		"echo duplicate",
		"echo duplicate",
		"echo $SELECTED_DATABASE env.SELECTED_DATABASE ${SELECTED_DATABASE} DATABASE_SUFFIX",
	}
	if got := result.Config.Tasks["build"].Run; !reflect.DeepEqual(got, wantRun) {
		t.Fatalf("build run = %#v, want %#v", got, wantRun)
	}
	if result.Config.Env["DATABASE"] != "shared" || result.Config.Env["SELECTED_DATABASE"] != "selected" {
		t.Fatalf("environment = %#v", result.Config.Env)
	}
	settings := result.Config.Root["settings"].(map[string]any)
	if got := settings["values"]; !reflect.DeepEqual(got, []any{"a", "b", "c"}) {
		t.Fatalf("settings.values = %#v", got)
	}
	if settings["color"] != "green" {
		t.Fatalf("settings.color = %#v", settings["color"])
	}
	if !strings.Contains(string(result.Bytes), "SELECTED_DATABASE") {
		t.Fatalf("canonical bytes omitted rename:\n%s", result.Bytes)
	}
}

func TestMergeMiseConfigsAppliesEnvironmentRenamesBeforeMergingValues(t *testing.T) {
	shared, err := ExtractMiseConfig("shared", []byte(`[env]
Z = "shared"
`))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ExtractMiseConfig("selected", []byte(`[env]
A = "$Z ${Z} env.Z"
Z = "selected"

[tasks.show]
run = "echo $A $Z"
`))
	if err != nil {
		t.Fatal(err)
	}
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{
		"Z": {Choice: MergeChoiceRenameSelected, Rename: "SELECTED_Z"},
	}}
	result, err := MergeMiseConfigs(shared, selected, "selected", resolver)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Config.Env["A"], "$SELECTED_Z ${SELECTED_Z} env.SELECTED_Z"; got != want {
		t.Fatalf("A = %#v, want %q", got, want)
	}
	if got := result.Config.Env["SELECTED_Z"]; got != "selected" {
		t.Fatalf("SELECTED_Z = %#v, want selected", got)
	}
	if _, exists := result.Config.Env["Z"]; !exists || result.Config.Env["Z"] != "shared" {
		t.Fatalf("environment = %#v", result.Config.Env)
	}
	if got, want := result.Config.Tasks["show"].Run, []string{"echo $A $SELECTED_Z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("show run = %#v, want %#v", got, want)
	}
}

func TestMergeMiseConfigsTracksCompleteStructuredToolChanges(t *testing.T) {
	shared, err := ExtractMiseConfig("shared", []byte(`[tools]
node = { version = "22", os = ["linux", "macos"] }
`))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ExtractMiseConfig("selected", []byte(`[tools]
node = { version = "20", os = ["linux"] }
`))
	if err != nil {
		t.Fatal(err)
	}
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{
		"tools.node": {Choice: MergeChoiceKeepShared},
	}}
	result, err := MergeMiseConfigs(shared, selected, "selected", resolver)
	if err != nil {
		t.Fatal(err)
	}
	want := []ToolChange{{
		Name: "node",
		From: map[string]any{"version": "20", "os": []any{"linux"}},
		To:   map[string]any{"version": "22", "os": []any{"linux", "macos"}},
	}}
	if !reflect.DeepEqual(result.ToolChanges, want) {
		t.Fatalf("tool changes = %#v, want %#v", result.ToolChanges, want)
	}
}

func TestMergeMiseConfigsRejectsDifferentToolMajors(t *testing.T) {
	shared, err := ExtractMiseConfig("shared", []byte("[tools]\nnode = '22'\n"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ExtractMiseConfig("selected", []byte("[tools]\nnode = '20'\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = MergeMiseConfigs(shared, selected, "selected", nil)
	if err == nil || !strings.Contains(err.Error(), "incompatible major versions 22 and 20") {
		t.Fatalf("error = %v", err)
	}
}

func TestMergeMiseConfigsAsksForIncomparableToolSelectors(t *testing.T) {
	shared, err := ExtractMiseConfig("shared", []byte("[tools]\nnode = 'lts'\n"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ExtractMiseConfig("selected", []byte("[tools]\nnode = 'latest'\n"))
	if err != nil {
		t.Fatal(err)
	}
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{"tools.node": {Choice: MergeChoiceKeepShared}}}
	result, err := MergeMiseConfigs(shared, selected, "selected", resolver)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Tools["node"].Selector != "lts" {
		t.Fatalf("node = %#v", result.Config.Tools["node"])
	}
}
