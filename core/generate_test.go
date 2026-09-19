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

func TestGenerateCopiesSharedFilesBeforeTemplateOverlay(t *testing.T) {
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
	if err := Generate(context.Background(), workspace, selector, fixedQuestionnaire{"name": "orders"}, io.Discard); err != nil {
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
	if got, want := string(overlay), "[tasks.build]\nrun = 'echo ok'\n"; got != want {
		t.Fatalf("overlaid file = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(destination, "premise-rust-lib")); err == nil {
		t.Fatal("template directory was copied as shared content")
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect unexpected shared directory: %v", err)
	}
}
