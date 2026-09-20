package core

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Resolution helpers
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
// Plan construction
// -----------------------------------------------------------------------------

func TestBuildMergePlanSortsAndResolvesFocusedCollisions(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "templates")
	selected := filepath.Join(shared, "app")
	if err := os.MkdirAll(filepath.Join(selected, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(shared, ".gitignore"), "shared-placeholder\n", 0o644)
	writeTestFile(t, filepath.Join(shared, "NOTICE"), "shared\n", 0o640)
	writeTestFile(t, filepath.Join(selected, ".gitignore"), "!shared-placeholder\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "NOTICE"), "selected\n", 0o600)
	writeTestFile(t, filepath.Join(selected, "main.txt"), "hello shared-placeholder\n", 0o644)
	writeTestFile(t, filepath.Join(shared, "sibling", "ignored.txt"), "ignored\n", 0o644)
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{"NOTICE": {Choice: MergeChoiceKeepShared}}}
	destination := filepath.Join(root, "output")

	plan, err := BuildMergePlan(TemplateGeneration{RegistryTemplatesRoot: shared, TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: "output", Substitutions: map[string]string{"shared-placeholder": "orders"}, TemplateKind: "app", TemplateIdentity: "app", ResolveConflict: resolver.ResolveMergeConflict})
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(plan.Entries))
	for index, entry := range plan.Entries {
		paths[index] = entry.Path
	}
	wantPaths := []string{".gitignore", "NOTICE", "empty", "main.txt"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	if got := string(plan.Entries[0].Output.Data); got != "orders\n!orders\n" {
		t.Fatalf("merged .gitignore = %q", got)
	}
	if plan.Entries[0].Strategy != MergeStrategyOrderedLines || plan.Entries[1].Strategy != MergeStrategyWholeFile {
		t.Fatalf("unexpected strategies: %#v", plan.Entries)
	}
	if got := string(plan.Entries[1].Output.Data); got != "shared\n" || plan.Entries[1].Output.Mode.Perm() != 0o640 {
		t.Fatalf("NOTICE output = %q mode %o", got, plan.Entries[1].Output.Mode.Perm())
	}

	if err := materializeMergePlan(plan, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "hello orders\n" {
		t.Fatalf("main.txt = %q", got)
	}
	if info, err := os.Stat(filepath.Join(destination, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory missing: %v", err)
	}
}

func TestBuildMergePlanPrunesSelectedDirectoryWhenSharedFileWins(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(shared, "config"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "config", "nested.txt"), "selected\n", 0o644)
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{
		"config": {Choice: MergeChoiceKeepShared},
	}}
	destination := filepath.Join(root, "output")

	plan, err := BuildMergePlan(TemplateGeneration{RegistryTemplatesRoot: shared, TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: "output", TemplateIdentity: "selected", ResolveConflict: resolver.ResolveMergeConflict})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(plan.Entries), 1; got != want {
		t.Fatalf("entries = %#v, want %d entry", plan.Entries, want)
	}
	if entry := plan.Entries[0]; entry.Path != "config" || entry.Output.Kind != "file" {
		t.Fatalf("config entry = %#v", entry)
	}

	if err := materializeMergePlan(plan, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "shared\n" {
		t.Fatalf("config = %q, want shared", got)
	}
}

func TestBuildMergePlanDoesNotCoalesceEqualBytesWithDifferentModes(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	selected := filepath.Join(root, "selected")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(shared, "same"), "same\n", 0o600)
	writeTestFile(t, filepath.Join(selected, "same"), "same\n", 0o644)
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{"same": {Choice: MergeChoiceUseSelected}}}
	plan, err := BuildMergePlan(TemplateGeneration{RegistryTemplatesRoot: shared, TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: "output", TemplateIdentity: "selected", ResolveConflict: resolver.ResolveMergeConflict})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Entries[0].Strategy != MergeStrategyWholeFile || plan.Entries[0].Output.Mode.Perm() != 0o644 {
		t.Fatalf("equal bytes with differing modes were silently coalesced: %#v", plan.Entries[0])
	}
	if len(resolver.Calls) != 1 {
		t.Fatalf("resolver calls = %d, want 1", len(resolver.Calls))
	}
}

func TestBuildMergePlanRejectsEmptyProjectPathWithoutStaging(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	temp := filepath.Join(root, "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("TMPDIR", temp)

	_, err := BuildMergePlan(TemplateGeneration{TemplateRoot: selected, ClientRepoRoot: root})
	if err == nil || !strings.Contains(err.Error(), "project path is required") {
		t.Fatalf("error = %v, want required project path error", err)
	}
	if matches, globErr := filepath.Glob(filepath.Join(root, ".premise-stage-*")); globErr != nil || len(matches) != 0 {
		t.Fatalf("merge stages = %v, err = %v", matches, globErr)
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("selected stages = %v", entries)
	}
}

func TestBuildMergePlanRejectsInvalidProjectPaths(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	for _, projectPath := range []string{filepath.Join(root, "absolute"), filepath.Join("..", "outside")} {
		_, err := BuildMergePlan(TemplateGeneration{TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: projectPath})
		if err == nil || !strings.Contains(err.Error(), "project path") {
			t.Fatalf("project path %q error = %v, want project path validation error", projectPath, err)
		}
	}
}

func TestBuildMergePlanRejectsInvalidClientRepoRoot(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	_, err := BuildMergePlan(TemplateGeneration{TemplateRoot: selected, ProjectPath: "apps/orders"})
	if err == nil || !strings.Contains(err.Error(), "client repository root is required") {
		t.Fatalf("error = %v, want required client root error", err)
	}

	fileRoot := filepath.Join(root, "client-file")
	writeTestFile(t, fileRoot, "not a directory\n", 0o644)
	_, err = BuildMergePlan(TemplateGeneration{TemplateRoot: selected, ClientRepoRoot: fileRoot, ProjectPath: "apps/orders"})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %v, want non-directory client root error", err)
	}
}

func TestBuildMergePlanLeavesDestinationAbsent(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	plan, err := BuildMergePlan(TemplateGeneration{TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: filepath.Join("workspace", "apps", "orders")})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after build: %v", err)
	}
	if _, err := os.Stat(plan.stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plan.selectedStage, "main.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plan.stage, "main.txt")); !os.IsNotExist(err) {
		t.Fatalf("stage was materialized during build: %v", err)
	}
}

func TestBuildMergePlanPreservesSelectedRootMode(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "selected\n", 0o644)
	if err := os.Chmod(selected, 0o750); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildMergePlan(TemplateGeneration{TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: filepath.Join("workspace", "apps", "orders")})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if got, want := plan.Root.Mode.Perm(), os.FileMode(0o750); got != want {
		t.Fatalf("planned root mode = %o, want %o", got, want)
	}
	if err := materializeMergePlan(plan, plan.stage); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(plan.stage)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Fatalf("staged root mode = %o, want 750", got)
	}
}

func TestBuildMergePlanRejectsDestinationInsideSource(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := BuildMergePlan(TemplateGeneration{
		TemplateRoot:   selected,
		ClientRepoRoot: selected,
		ProjectPath:    "output",
	})
	if err == nil || !strings.Contains(err.Error(), "inside source") {
		t.Fatalf("error = %v, want destination containment error", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(selected, ".premise-stage-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("source stages = %v, err = %v", matches, globErr)
	}
}

// -----------------------------------------------------------------------------
// Plan execution and cleanup
// -----------------------------------------------------------------------------

func TestExecuteMergePlanCreatesDestinationAndCleansStages(t *testing.T) {
	installMiseTestShim(t, false)
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	plan, err := BuildMergePlan(TemplateGeneration{TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: filepath.Join("workspace", "apps", "orders"), TemplateKind: "app"})
	if err != nil {
		t.Fatal(err)
	}
	stage, selectedStage := plan.stage, plan.selectedStage
	if err := ExecuteMergePlan(context.Background(), plan, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "main.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage leaked: %v", err)
	}
	if _, err := os.Stat(selectedStage); !os.IsNotExist(err) {
		t.Fatalf("selected stage leaked: %v", err)
	}
}

func TestBuildMergePlanCleansStagesWhenConflictResolutionFails(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "registry", "templates")
	selected := filepath.Join(shared, "app")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(shared, "NOTICE"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "NOTICE"), "selected\n", 0o644)
	if _, err := BuildMergePlan(TemplateGeneration{
		RegistryTemplatesRoot: shared, TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: filepath.Join("workspace", "apps", "orders"),
		ResolveConflict: func(MergeConflict) (MergeDecision, error) { return MergeDecision{Choice: MergeChoiceAbort}, nil },
	}); err == nil {
		t.Fatal("build succeeded, want conflict abort")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".premise-stage-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("sibling stages = %v, err = %v", matches, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failed build: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Dependency and candidate validation
// -----------------------------------------------------------------------------

func TestValidateMergeToolChangesRunsEachChangeInDisposableCopy(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nbun = '1.1'\n", 0o644)
	log := installMiseTestShim(t, false)
	if err := validateMergeToolChanges(context.Background(), selected, "lib", []ToolChange{{Name: "bun", From: "1.1", To: "1.2"}}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	tasks, err := ContractTasks("lib")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(lines), 1+len(tasks); got != want {
		t.Fatalf("mise calls = %d, want %d:\n%s", got, want, data)
	}
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || parts[1] != "1" || parts[0] == selected {
			t.Fatalf("unexpected isolated invocation %q", line)
		}
	}
	original, err := os.ReadFile(filepath.Join(selected, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "[tools]\nbun = '1.1'\n" {
		t.Fatalf("selected template mutated:\n%s", original)
	}
}

func TestValidateMergeToolChangesAppliesCompleteStructuredValue(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nnode = { version = '20', os = ['linux'] }\n", 0o644)
	captured := filepath.Join(t.TempDir(), "mise.toml")
	t.Setenv("MISE_CONFIG_LOG", captured)
	installMiseTestShim(t, false)
	change := ToolChange{
		Name: "node",
		From: map[string]any{"version": "20", "os": []any{"linux"}},
		To:   map[string]any{"version": "22", "os": []any{"linux", "macos"}},
	}
	if err := validateMergeToolChanges(context.Background(), selected, "lib", []ToolChange{change}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ExtractMiseConfig("captured preflight", data)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Tools["node"].Raw; !reflect.DeepEqual(got, change.To) {
		t.Fatalf("preflight node = %#v, want %#v", got, change.To)
	}
}

func TestValidateMergeToolChangesAttributesFailureAndCleansCopy(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nbun = '1.1'\n", 0o644)
	temp := filepath.Join(t.TempDir(), "preflights")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)
	installMiseTestShim(t, true)
	err := validateMergeToolChanges(context.Background(), selected, "lib", []ToolChange{{Name: "bun", From: "1.1", To: "1.2"}}, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "tool update bun 1.1 -> 1.2 failed") {
		t.Fatalf("error = %v", err)
	}
	entries, readErr := os.ReadDir(temp)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("preflight copies leaked: %v", entries)
	}
}

func TestValidateMergeCandidateRunsContractsInDisposableCopy(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(stage, "mise.toml"), "[tasks.build]\nrun = 'echo build'\n", 0o644)
	writeTestFile(t, filepath.Join(stage, "marker.txt"), "unchanged\n", 0o600)
	temporary := filepath.Join(t.TempDir(), "candidates")
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temporary)
	log := installMiseTestShim(t, false)

	if err := validateMergeCandidate(context.Background(), stage, "app", os.Stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	tasks, err := ContractTasks("app")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(lines), 1+len(tasks); got != want {
		t.Fatalf("mise calls = %d, want %d:\n%s", got, want, data)
	}
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || parts[1] != "1" || parts[0] == stage || !strings.Contains(parts[0], "premise-candidate-validation-") {
			t.Fatalf("unexpected candidate invocation %q", line)
		}
	}
	assertDirectoryEmpty(t, temporary)
	marker, err := os.ReadFile(filepath.Join(stage, "marker.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != "unchanged\n" {
		t.Fatalf("stage changed: %q", marker)
	}
}

func TestValidateMergeCandidateCleansCopyOnFailure(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(stage, "mise.toml"), "[tasks.build]\nrun = 'echo build'\n", 0o644)
	temporary := filepath.Join(t.TempDir(), "candidates")
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temporary)
	installMiseTestShim(t, true)

	err := validateMergeCandidate(context.Background(), stage, "lib", os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "generated candidate validation failed") {
		t.Fatalf("error = %v", err)
	}
	assertDirectoryEmpty(t, temporary)
}

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary directories leaked in %s: %v", path, entries)
	}
}

func installMiseTestShim(t *testing.T, fail bool) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "mise.log")
	script := "#!/bin/sh\nprintf '%s|%s|%s\\n' \"$PWD\" \"$PREMISE_TEMPLATE_TEST\" \"$*\" >> \"$MISE_LOG\"\nif [ -n \"$MISE_CONFIG_LOG\" ] && [ ! -e \"$MISE_CONFIG_LOG\" ]; then cp \"$PWD/mise.toml\" \"$MISE_CONFIG_LOG\"; fi\n"
	if fail {
		script += "exit 7\n"
	} else {
		script += "exit 0\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISE_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}
