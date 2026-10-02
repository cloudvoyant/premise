package backends

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

func TestCargoReleaseArtifactsExcludeDirectTauriApps(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	publicCLI := templateFixture("public-cli", "app")
	privateCLI := templateFixture("private-cli", "app")
	tauri := templateFixture("tauri-app", "app")
	manifest.TemplateRegistry = &TemplateRegistry{
		WorkspaceFiles: []string{},
		Templates:      []Template{publicCLI, privateCLI, tauri},
	}
	for _, template := range manifest.TemplateRegistry.Templates {
		path := filepath.Join(root, filepath.FromSlash(template.Path))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		cargo := "[package]\nname = \"" + template.Name + "\"\nversion = \"0.1.0\"\n"
		if template.Name != "public-cli" {
			cargo += "publish = false\n"
		}
		if err := os.WriteFile(filepath.Join(path, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(tauri.Path), "tauri.conf.json"), []byte(`{"productName":"tauri-app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ManifestFilename)
	if err := SaveManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := core.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := (Cargo{}).CreateGoReleaserConfig(root, loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"public-cli", "private-cli"} {
		if !strings.Contains(config, "binary: "+name) || !strings.Contains(config, "- id: "+name) {
			t.Fatalf("missing app %s from release configuration: %s", name, config)
		}
	}
	if strings.Contains(config, "tauri-app") {
		t.Fatalf("Tauri app was included in generic archives: %s", config)
	}
	if got := strings.Count(config, "x86_64-unknown-linux-gnu"); got != 2 {
		t.Fatalf("Linux x86-64 target count = %d, want 2", got)
	}
}
