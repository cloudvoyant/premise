package core

import (
	"reflect"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Runner environment
// -----------------------------------------------------------------------------

func TestWithoutEnvironment(t *testing.T) {
	environment := []string{"PATH=/bin", "GITHUB_TOKEN=github", "CARGO_REGISTRY_TOKEN=cargo", "VALUE=a=b"}
	filtered := withoutEnvironment(environment, "GITHUB_TOKEN", "CARGO_REGISTRY_TOKEN")
	if got := strings.Join(filtered, "\n"); got != "PATH=/bin\nVALUE=a=b" {
		t.Fatalf("WithoutEnvironment() = %q", got)
	}
}

func TestRunnerEnvironmentReplacesManagedValues(t *testing.T) {
	t.Setenv("MISE_CEILING_PATHS", "/old")
	t.Setenv("PREMISE_TEMPLATE_TEST", "0")
	for _, name := range publicationCredentialEnvironment {
		t.Setenv(name, "secret")
	}
	runner := miseRunner{Ceiling: "/workspace"}
	environment := runner.environment([]string{"PREMISE_TEMPLATE_TEST=1"})
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, want := range []string{"\nMISE_CEILING_PATHS=/workspace\n", "\nPREMISE_TEMPLATE_TEST=1\n"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Environment() omitted %q", want)
		}
	}
	for _, unwanted := range []string{
		"\nMISE_CEILING_PATHS=/old\n",
		"\nPREMISE_TEMPLATE_TEST=0\n",
		"\nGITHUB_TOKEN=secret\n",
		"\nGH_TOKEN=secret\n",
		"\nCARGO_REGISTRY_TOKEN=secret\n",
		"\nCARGO_TOKEN=secret\n",
		"\nCRATES_TOKEN=secret\n",
	} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("Environment() retained %q", unwanted)
		}
	}
}

// -----------------------------------------------------------------------------
// Typed configuration
// -----------------------------------------------------------------------------

func TestExtractMiseConfigNormalizesTasksAndPreservesUnknownRoot(t *testing.T) {
	config, err := ExtractMiseConfig("selected", []byte(`min_version = "2026.1"
unknown = { enabled = true }

[tools]
go = "1.25"

[tasks]
build = "go build ./..."
test = { run = ["go test ./...", "echo done"], description = "test" }

[env]
MODE = "test"
`))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Tools["go"].StringSelector || config.Tools["go"].Selector != "1.25" {
		t.Fatalf("unexpected tool: %#v", config.Tools["go"])
	}
	if !config.Tasks["build"].Shorthand || len(config.Tasks["build"].Run) != 1 {
		t.Fatalf("unexpected shorthand task: %#v", config.Tasks["build"])
	}
	if len(config.Tasks["test"].Run) != 2 || config.Env["MODE"] != "test" {
		t.Fatalf("unexpected typed views: tasks=%#v env=%#v", config.Tasks, config.Env)
	}
	encoded, err := encodeMiseConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "[unknown]") || !strings.Contains(string(encoded), "[tasks.build]") {
		t.Fatalf("canonical output lost normalized or unknown values:\n%s", encoded)
	}
}

func TestExtractMiseConfigRejectsMalformedSectionsIndependently(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{name: "tools", data: `tools = "go"`, want: "shared mise.toml section tools must be a table"},
		{name: "tasks", data: `tasks = ["build"]`, want: "shared mise.toml section tasks must be a table"},
		{name: "env", data: `env = "test"`, want: "shared mise.toml section env must be a table"},
		{name: "task run", data: "[tasks.build]\nrun = 1\n", want: "shared mise.toml task build run must be a string or string list"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ExtractMiseConfig("shared", []byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Semantic merge
// -----------------------------------------------------------------------------

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
	resolver := MergeDecisions{
		"DATABASE":       {Choice: MergeChoiceRenameSelected, Rename: "SELECTED_DATABASE"},
		"settings.color": {Choice: MergeChoiceUseSelected},
	}
	result, err := MergeMiseConfigs(shared, selected, "lib", "selected", resolver.Resolve)
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
	resolver := MergeDecisions{
		"Z": {Choice: MergeChoiceRenameSelected, Rename: "SELECTED_Z"},
	}
	result, err := MergeMiseConfigs(shared, selected, "lib", "selected", resolver.Resolve)
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
	resolver := MergeDecisions{
		"tools.node": {Choice: MergeChoiceKeepShared},
	}
	result, err := MergeMiseConfigs(shared, selected, "lib", "selected", resolver.Resolve)
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

func TestMergeMiseConfigsPreservesSelectedContractMetadata(t *testing.T) {
	shared, err := ExtractMiseConfig("shared", []byte(`[tasks.build]
run = ["echo shared"]
args = ["shared"]
flags = ["--shared"]
env = { SIDE = "shared" }
`))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ExtractMiseConfig("selected", []byte(`[tasks.build]
run = "echo selected"
description = "selected description"
args = ["selected"]
flags = ["--selected"]
env = { SIDE = "selected" }
`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := MergeMiseConfigs(shared, selected, "lib", "owner/repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	task := result.Config.Tasks["build"]
	if got, want := task.Run, []string{"echo shared", "echo selected"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("build run = %#v, want %#v", got, want)
	}
	if task.Raw["description"] != "selected description" || !reflect.DeepEqual(task.Raw["args"], []any{"selected"}) || !reflect.DeepEqual(task.Raw["flags"], []any{"--selected"}) {
		t.Fatalf("selected task metadata was not preserved: %#v", task.Raw)
	}
	if _, exists := task.Raw["env"].(map[string]any); !exists {
		t.Fatalf("selected task env missing: %#v", task.Raw)
	}
}

func TestMergeMiseConfigsNamespacesNonContractTasks(t *testing.T) {
	shared, err := ExtractMiseConfig("shared", []byte(`[tasks.custom]
run = "echo shared"
description = "shared"
`))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ExtractMiseConfig("selected", []byte(`[tasks.custom]
run = ["echo selected"]
description = "selected"
`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := MergeMiseConfigs(shared, selected, "lib", "owner/repo.git", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Config.Tasks["custom"].Raw["description"]; got != "shared" {
		t.Fatalf("shared task = %#v, want shared definition", result.Config.Tasks["custom"].Raw)
	}
	if got := result.Config.Tasks["repo:custom"].Raw["description"]; got != "selected" {
		t.Fatalf("selected task = %#v, want namespaced definition", result.Config.Tasks["repo:custom"].Raw)
	}
	wantNotice := `Mise task "custom" was namespaced as "repo:custom" to preserve the shared task`
	if !reflect.DeepEqual(result.Notices, []string{wantNotice}) {
		t.Fatalf("notices = %#v, want %#v", result.Notices, []string{wantNotice})
	}
}

func TestMergeMiseConfigsRejectsNamespacedTaskCollision(t *testing.T) {
	shared, err := ExtractMiseConfig("shared", []byte(`[tasks.custom]
run = "echo shared"
[tasks."repo:custom"]
run = "echo collision"
`))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := ExtractMiseConfig("selected", []byte(`[tasks.custom]
run = "echo selected"
`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = MergeMiseConfigs(shared, selected, "lib", "owner/repo", nil)
	if err == nil || !strings.Contains(err.Error(), `task name already exists`) {
		t.Fatalf("error = %v, want namespaced collision", err)
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
	_, err = MergeMiseConfigs(shared, selected, "lib", "selected", nil)
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
	resolver := MergeDecisions{"tools.node": {Choice: MergeChoiceKeepShared}}
	result, err := MergeMiseConfigs(shared, selected, "lib", "selected", resolver.Resolve)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Tools["node"].Selector != "lts" {
		t.Fatalf("node = %#v", result.Config.Tools["node"])
	}
}
