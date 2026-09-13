package core

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type fixedQuestionnaire map[string]string

func (answers fixedQuestionnaire) Ask(_ []Question) (map[string]string, error) {
	response := make(map[string]string, len(answers))
	for name, value := range answers {
		response[name] = value
	}
	return response, nil
}

func TestGenerateRecordsQualifiedSelector(t *testing.T) {
	cargoRegistry := writeRegistryFixture(t, templateFixture("premise-rust-lib", "lib"))
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := InitializeWorkspace(workspace, "[tasks.build]\nrun = 'echo ok'\n"); err != nil {
		t.Fatal(err)
	}

	const selector = "cloudvoyant/premise-cargo:premise-rust-lib"
	if err := Generate(context.Background(), workspace, selector, fixedQuestionnaire{"name": "orders"}, io.Discard); err != nil {
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
