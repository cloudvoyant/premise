package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Fixtures -------------------------------------------------------------------

type generationTestProvider struct {
	testPackageManager
	prepared *bool
}

// Tests ----------------------------------------------------------------------

func TestGenerationUsesSelectedProviderCapabilities(t *testing.T) {
	prepared := false
	provider := generationTestProvider{testPackageManager: testPackageManager{id: "future"}, prepared: &prepared}
	useTestPackageManagers(t, provider)
	root, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("project\n"), 0644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildGeneratePlan(GenerateParameters{PackageManagers: []string{"future"}, ClientRepoRoot: root, TemplateRoot: source, ProjectPath: "libs/example", TemplateKind: "lib"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if err := prepareWorkspaceCandidate(plan, t.TempDir(), true); err != nil {
		t.Fatal(err)
	}

	if !prepared || len(plan.Entries) != 1 || plan.Entries[0].Path != "future.lock" {
		t.Fatalf("selected provider was bypassed: %v", plan.Entries)
	}
	if _, err := os.Stat(filepath.Join(root, "future.lock")); !os.IsNotExist(err) {
		t.Fatal("planning modified the workspace")
	}
}

func TestUnrelatedGenerationDoesNotInspectGoWorkspace(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.25.0\nuse /outside/workspace\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"name":"web"}`), 0644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildGeneratePlan(GenerateParameters{ClientRepoRoot: root, TemplateRoot: source, ProjectPath: "apps/web", TemplateKind: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	err = prepareWorkspaceCandidate(plan, t.TempDir(), true)

	if err != nil {
		t.Fatalf("unrelated generation applied Go workspace rules: %v", err)
	}
}

// Private helpers ------------------------------------------------------------

func (provider generationTestProvider) PrepareGeneration(request PackageGenerationContext) ([]MergeEntry, error) {
	return []MergeEntry{{Path: "future.lock", Selected: PathValue{Kind: "file", Mode: 0644}, Output: PathValue{Present: true, Kind: "file", Mode: 0644, Data: []byte("dependencies\n")}}}, nil
}
func (provider generationTestProvider) PrepareGenerationCandidate(_ PackageGenerationContext, _ string) error {
	*provider.prepared = true
	return nil
}
