package core

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Generate API
// -----------------------------------------------------------------------------

func TestGenerateRecordsQualifiedSelector(t *testing.T) {
	installMiseTestShim(t, false)
	cargoRegistry := writeRegistryFixture(t, templateFixture("premise-rust-lib", "lib"))
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := InitializeWorkspace(workspace, "[tasks.build]\nrun = 'echo ok'\n"); err != nil {
		t.Fatal(err)
	}

	const selector = "cloudvoyant/premise-cargo:premise-rust-lib"
	if err := Generate(context.Background(), workspace, selector, fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard); err != nil {
		t.Fatal(err)
	}

	manifest, err := LoadManifest(filepath.Join(workspace, ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Workspace.Projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(manifest.Workspace.Projects))
	}
	project := manifest.Workspace.Projects[0]
	if project.Template != selector {
		t.Fatalf("recorded template = %q, want %q", project.Template, selector)
	}
	if project.Path != "libs/orders" {
		t.Fatalf("recorded path = %q, want libs/orders", project.Path)
	}
	if _, err := os.Stat(filepath.Join(workspace, "libs", "orders")); err != nil {
		t.Fatalf("generated destination missing: %v", err)
	}
}

func TestGenerateMergesSharedFilesWithSelectedTemplate(t *testing.T) {
	installMiseTestShim(t, false)
	template := templateFixture("premise-rust-lib", "lib")
	template.Substitutions = map[string]string{"shared-placeholder": "name"}
	cargoRegistry := writeRegistryFixture(t, template)
	templatesRoot := filepath.Join(cargoRegistry, "templates")
	if err := os.WriteFile(filepath.Join(templatesRoot, ".shared-config"), []byte("name=shared-placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templatesRoot, "mise.toml"), []byte("[tasks.build]\nrun = 'echo shared'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := InitializeWorkspace(workspace, "[tasks.build]\nrun = 'echo ok'\n"); err != nil {
		t.Fatal(err)
	}

	const selector = "cloudvoyant/premise-cargo:premise-rust-lib"
	if err := Generate(context.Background(), workspace, selector, fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(workspace, "libs", "orders")
	shared, err := os.ReadFile(filepath.Join(destination, ".shared-config"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(shared), "name=orders\n"; got != want {
		t.Fatalf("shared file = %q, want %q", got, want)
	}
	overlay, err := os.ReadFile(filepath.Join(destination, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(overlay), "[tasks]\n[tasks.build]\nrun = ['echo shared', 'echo ok']\n"; got != want {
		t.Fatalf("merged mise.toml = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(destination, "premise-rust-lib")); err == nil {
		t.Fatal("template directory was copied as shared content")
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect unexpected shared directory: %v", err)
	}
}

func TestGenerateDoesNotPrintTechnicalCollisionComparison(t *testing.T) {
	installMiseTestShim(t, false)
	template := templateFixture("app", "app")
	registry := writeRegistryFixture(t, template)
	shared := filepath.Join(registry, "templates")
	selected := filepath.Join(shared, "app")
	writeTestFile(t, filepath.Join(shared, ".gitattributes"), "*.txt text\n", 0o644)
	writeTestFile(t, filepath.Join(selected, ".gitattributes"), "*.sh text eol=lf\n", 0o644)
	writeTestFile(t, filepath.Join(shared, ".gitignore"), "shared/\n", 0o644)
	writeTestFile(t, filepath.Join(selected, ".gitignore"), "selected/\n", 0o644)
	writeTestFile(t, filepath.Join(shared, "NOTICE"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "NOTICE"), "selected\n", 0o644)
	writeTestFile(t, filepath.Join(shared, "mise.toml"), "[tools]\ngo = '1.25'\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\ngo = '1.24'\n", 0o644)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := InitializeWorkspace(workspace, "monorepo_root = true\n"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Generate(context.Background(), workspace, registry+":app", GenerateOptions{
		Questionnaire:    fixedQuestionnaire{"name": "orders"},
		ConflictResolver: MergeDecisions{"NOTICE": {Choice: MergeChoiceKeepShared}}.Resolve,
	}, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Template root comparison:") || strings.Contains(output.String(), "no collisions") {
		t.Fatalf("technical comparison output was not removed:\n%s", output.String())
	}
}

func TestGenerateToolPreflightFailureLeavesDestinationAndManifestUntouched(t *testing.T) {
	installScopedMiseTestShim(t, "premise-tool-preflight-")
	registry := writeRegistryFixture(t, templateFixture("app", "app"))
	shared := filepath.Join(registry, "templates")
	selected := filepath.Join(shared, "app")
	writeTestFile(t, filepath.Join(shared, "mise.toml"), "[tools]\nbun = '1.2'\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nbun = '1.1'\n", 0o644)

	workspace := filepath.Join(t.TempDir(), "workspace")
	manifestPath, err := InitializeWorkspace(workspace, "monorepo_root = true\n")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(t.TempDir(), "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)

	err = Generate(context.Background(), workspace, registry+":app", fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "tool update bun 1.1 -> 1.2 failed") {
		t.Fatalf("error = %v", err)
	}
	assertGenerationFailureCleanup(t, workspace, filepath.Join("apps", "orders"), manifestPath, before, temp)
}

func TestGenerateCandidateFailureLeavesDestinationAndManifestUntouched(t *testing.T) {
	installScopedMiseTestShim(t, "premise-candidate-validation-")
	registry := writeRegistryFixture(t, templateFixture("app", "app"))
	workspace := filepath.Join(t.TempDir(), "workspace")
	manifestPath, err := InitializeWorkspace(workspace, "monorepo_root = true\n")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(t.TempDir(), "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)

	err = Generate(context.Background(), workspace, registry+":app", fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "generated candidate validation failed") {
		t.Fatalf("error = %v", err)
	}
	assertGenerationFailureCleanup(t, workspace, filepath.Join("apps", "orders"), manifestPath, before, temp)
}

func TestGenerateProjectRegistrationFailureRemovesCommittedDestination(t *testing.T) {
	installMiseTestShim(t, false)
	registry := writeRegistryFixture(t, templateFixture("app", "app"))
	workspace := filepath.Join(t.TempDir(), "workspace")
	manifestPath, err := InitializeWorkspace(workspace, "monorepo_root = true\n")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: "orders", Template: "old:app", Path: "apps/orders"})
	if err := SaveManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	err = Generate(context.Background(), workspace, registry+":app", fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "project \"orders\" is already registered") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "apps", "orders")); !os.IsNotExist(err) {
		t.Fatalf("destination exists after registration failure: %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("manifest changed after registration failure:\n%s", after)
	}
}

// -----------------------------------------------------------------------------
// Plan construction
// -----------------------------------------------------------------------------

func TestBuildGeneratePlanSortsAndResolvesFocusedConflicts(t *testing.T) {
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
	resolver := MergeDecisions{"NOTICE": {Choice: MergeChoiceKeepShared}}
	destination := filepath.Join(root, "output")

	plan, err := BuildGeneratePlan(GenerateParameters{RegistryTemplatesRoot: shared, TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: "output", Substitutions: map[string]string{"shared-placeholder": "orders"}, TemplateKind: "app", TemplateIdentity: "app", ResolveConflict: resolver.Resolve})
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

	if err := materializeGeneratePlan(plan, destination); err != nil {
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

func TestBuildGeneratePlanPrunesSelectedDirectoryWhenSharedFileWins(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(shared, "config"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "config", "nested.txt"), "selected\n", 0o644)
	resolver := MergeDecisions{
		"config": {Choice: MergeChoiceKeepShared},
	}
	destination := filepath.Join(root, "output")

	plan, err := BuildGeneratePlan(GenerateParameters{RegistryTemplatesRoot: shared, TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: "output", TemplateIdentity: "selected", ResolveConflict: resolver.Resolve})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(plan.Entries), 1; got != want {
		t.Fatalf("entries = %#v, want %d entry", plan.Entries, want)
	}
	if entry := plan.Entries[0]; entry.Path != "config" || entry.Output.Kind != "file" {
		t.Fatalf("config entry = %#v", entry)
	}

	if err := materializeGeneratePlan(plan, destination); err != nil {
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

func TestBuildGeneratePlanDoesNotCoalesceEqualBytesWithDifferentModes(t *testing.T) {
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
	decisions := MergeDecisions{"same": {Choice: MergeChoiceUseSelected}}
	calls := []MergeConflict{}
	var resolver MergeConflictResolver = func(conflict MergeConflict) (MergeDecision, error) {
		calls = append(calls, conflict)
		return decisions.Resolve(conflict)
	}
	plan, err := BuildGeneratePlan(GenerateParameters{RegistryTemplatesRoot: shared, TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: "output", TemplateIdentity: "selected", ResolveConflict: resolver})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Entries[0].Strategy != MergeStrategyWholeFile || plan.Entries[0].Output.Mode.Perm() != 0o644 {
		t.Fatalf("equal bytes with differing modes were silently coalesced: %#v", plan.Entries[0])
	}
	if len(calls) != 1 {
		t.Fatalf("resolver calls = %d, want 1", len(calls))
	}
}

func TestBuildGeneratePlanRejectsEmptyProjectPathWithoutStaging(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	temp := filepath.Join(root, "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("TMPDIR", temp)

	_, err := BuildGeneratePlan(GenerateParameters{TemplateRoot: selected, ClientRepoRoot: root})
	if err == nil || !strings.Contains(err.Error(), "project path is required") {
		t.Fatalf("error = %v, want required project path error", err)
	}
	if matches, globErr := filepath.Glob(filepath.Join(root, ".premise-stage-*")); globErr != nil || len(matches) != 0 {
		t.Fatalf("generation stages = %v, err = %v", matches, globErr)
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("selected stages = %v", entries)
	}
}

func TestBuildGeneratePlanRejectsInvalidProjectPaths(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	for _, projectPath := range []string{filepath.Join(root, "absolute"), filepath.Join("..", "outside")} {
		_, err := BuildGeneratePlan(GenerateParameters{TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: projectPath})
		if err == nil || !strings.Contains(err.Error(), "project path") {
			t.Fatalf("project path %q error = %v, want project path validation error", projectPath, err)
		}
	}
}

func TestBuildGeneratePlanRejectsInvalidClientRepoRoot(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	_, err := BuildGeneratePlan(GenerateParameters{TemplateRoot: selected, ProjectPath: "apps/orders"})
	if err == nil || !strings.Contains(err.Error(), "client repository root is required") {
		t.Fatalf("error = %v, want required client root error", err)
	}

	fileRoot := filepath.Join(root, "client-file")
	writeTestFile(t, fileRoot, "not a directory\n", 0o644)
	_, err = BuildGeneratePlan(GenerateParameters{TemplateRoot: selected, ClientRepoRoot: fileRoot, ProjectPath: "apps/orders"})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %v, want non-directory client root error", err)
	}
}

func TestBuildGeneratePlanLeavesDestinationAbsent(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	plan, err := BuildGeneratePlan(GenerateParameters{TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: filepath.Join("workspace", "apps", "orders")})
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

func TestBuildGeneratePlanPreservesSelectedRootMode(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "selected\n", 0o644)
	if err := os.Chmod(selected, 0o750); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildGeneratePlan(GenerateParameters{TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: filepath.Join("workspace", "apps", "orders")})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if got, want := plan.Root.Mode.Perm(), os.FileMode(0o750); got != want {
		t.Fatalf("planned root mode = %o, want %o", got, want)
	}
	if err := materializeGeneratePlan(plan, plan.stage); err != nil {
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

func TestBuildGeneratePlanRejectsDestinationInsideSource(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := BuildGeneratePlan(GenerateParameters{
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

func TestApplyGeneratePlanCreatesDestinationAndCleansStages(t *testing.T) {
	installMiseTestShim(t, false)
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	plan, err := BuildGeneratePlan(GenerateParameters{TemplateRoot: selected, ClientRepoRoot: root, ProjectPath: filepath.Join("workspace", "apps", "orders"), TemplateKind: "app"})
	if err != nil {
		t.Fatal(err)
	}
	stage, selectedStage := plan.stage, plan.selectedStage
	if err := ApplyGeneratePlan(context.Background(), plan, io.Discard); err != nil {
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

func TestBuildGeneratePlanCleansStagesWhenConflictResolutionFails(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "registry", "templates")
	selected := filepath.Join(shared, "app")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(shared, "NOTICE"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "NOTICE"), "selected\n", 0o644)
	if _, err := BuildGeneratePlan(GenerateParameters{
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

func TestValidateGenerationToolChangesRunsEachChangeInDisposableCopy(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nbun = '1.1'\n", 0o644)
	log := installMiseTestShim(t, false)
	if err := validateGenerationToolChanges(context.Background(), selected, "lib", []ToolChange{{Name: "bun", From: "1.1", To: "1.2"}}, os.Stderr); err != nil {
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

func TestValidateGenerationToolChangesAppliesCompleteStructuredValue(t *testing.T) {
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
	if err := validateGenerationToolChanges(context.Background(), selected, "lib", []ToolChange{change}, os.Stderr); err != nil {
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

func TestValidateGenerationToolChangesAttributesFailureAndCleansCopy(t *testing.T) {
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
	err := validateGenerationToolChanges(context.Background(), selected, "lib", []ToolChange{{Name: "bun", From: "1.1", To: "1.2"}}, os.Stderr)
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

func TestValidateGeneratedCandidateRunsContractsInDisposableCopy(t *testing.T) {
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

	if err := validateGeneratedCandidate(context.Background(), stage, "app", os.Stderr); err != nil {
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

func TestValidateGeneratedCandidateCleansCopyOnFailure(t *testing.T) {
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

	err := validateGeneratedCandidate(context.Background(), stage, "lib", os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "generated candidate validation failed") {
		t.Fatalf("error = %v", err)
	}
	assertDirectoryEmpty(t, temporary)
}

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

type fixedQuestionnaire map[string]string

func (answers fixedQuestionnaire) Ask(_ []Question) (map[string]string, error) {
	response := make(map[string]string, len(answers))
	for name, value := range answers {
		response[name] = value
	}
	return response, nil
}

func fixedGenerateOptions(answers fixedQuestionnaire) GenerateOptions {
	return GenerateOptions{
		Questionnaire:    answers,
		ConflictResolver: MergeDecisions{}.Resolve,
	}
}

func installScopedMiseTestShim(t *testing.T, failDirectoryFragment string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "mise.log")
	script := "#!/bin/sh\n" +
		"printf '%s|%s|%s\\n' \"$PWD\" \"$PREMISE_TEMPLATE_TEST\" \"$*\" >> \"$MISE_LOG\"\n" +
		"case \"$PWD\" in *\"$MISE_FAIL_DIRECTORY\"*) exit 7;; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISE_LOG", log)
	t.Setenv("MISE_FAIL_DIRECTORY", failDirectoryFragment)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func assertGenerationFailureCleanup(t *testing.T, workspace, relativeDestination, manifestPath string, before []byte, temp string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(workspace, relativeDestination)); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failure: %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("manifest changed after failed generation:\n%s", after)
	}
	for _, root := range []string{filepath.Join(workspace, filepath.Dir(relativeDestination)), temp} {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "premise-stage-") || strings.Contains(entry.Name(), "premise-selected-") || strings.Contains(entry.Name(), "premise-tool-preflight-") || strings.Contains(entry.Name(), "premise-candidate-validation-") {
				t.Fatalf("temporary directory leaked at %s", filepath.Join(root, entry.Name()))
			}
		}
	}
}

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
