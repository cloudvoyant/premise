package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

func TestBuiltinDetectionAndGoReleaserConfig(t *testing.T) {
	if err := RegisterBuiltins(); err != nil {
		t.Fatal(err)
	}
	goRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveManifest(filepath.Join(goRoot, core.ManifestFilename), core.NewManifest("premise")); err != nil {
		t.Fatal(err)
	}
	config, err := core.GoReleaserConfig(goRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"project_name: premise", "binary: premise", "{{ .ProjectName }}-{{ .Tag }}-", "mode: keep-existing", "replace_existing_artifacts: true"} {
		if !strings.Contains(string(config), want) {
			t.Errorf("Go config does not contain %q", want)
		}
	}

	cargoRoot := t.TempDir()
	manifest := core.NewManifest("premise-cargo")
	manifest.TemplateRegistry = &core.TemplateRegistry{WorkspaceFiles: []string{}, Templates: []core.Template{
		templateFixture("premise-rust-lib", "lib"),
		templateFixture("premise-rust-app", "app"),
		templateFixture("premise-clap-cli", "app"),
		templateFixture("premise-tauri-app", "app"),
	}}
	if err := core.SaveManifest(filepath.Join(cargoRoot, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cargoRoot, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"premise-rust-lib", "premise-rust-app", "premise-clap-cli"} {
		dir := filepath.Join(cargoRoot, "templates", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "[package]\nname = \"" + name + "\"\nversion = \"0.1.0\"\n"
		if name == "premise-clap-cli" {
			content += "publish = false\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(cargoRoot, "templates", "premise-tauri-app", "src-tauri")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "Cargo.toml"), []byte("[package]\nname = \"premise-tauri-app\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err = core.GoReleaserConfig(cargoRoot)
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, want := range []string{"id: premise-rust-app", "id: premise-clap-cli", "builder: rust", "x86_64-unknown-linux-gnu", "aarch64-apple-darwin"} {
		if !strings.Contains(text, want) {
			t.Errorf("Cargo config does not contain %q", want)
		}
	}
	for _, unwanted := range []string{"id: premise-rust-lib", "id: premise-tauri-app"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("Cargo config unexpectedly contains %q", unwanted)
		}
	}
	if count := strings.Count(text, "builder: rust"); count != 2 {
		t.Fatalf("Cargo config contains %d builds, want 2", count)
	}

	// One monorepo can match Go, Cargo, and Bun at the root. Build the Go
	// and Cargo artifacts together; Bun has no downloadable artifacts.
	if err := os.WriteFile(filepath.Join(cargoRoot, "go.mod"), []byte("module example.com/mixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cargoRoot, "package.json"), []byte(`{"name":"mixed"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cargoRoot, "bunfig.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	config, err = core.GoReleaserConfig(cargoRoot)
	if err != nil {
		t.Fatal(err)
	}
	mixed := string(config)
	for _, want := range []string{"main: .", "builder: rust", "id: premise-rust-app", "{{ .ProjectName }}-{{ .Tag }}-"} {
		if !strings.Contains(mixed, want) {
			t.Errorf("mixed config does not contain %q: %s", want, mixed)
		}
	}
	if count := strings.Count(mixed, "builder: rust"); count != 2 {
		t.Fatalf("mixed config contains %d Rust builds, want 2", count)
	}

	root := t.TempDir()
	noDirectApp := core.NewManifest("premise-cargo")
	noDirectApp.TemplateRegistry = &core.TemplateRegistry{WorkspaceFiles: []string{}, Templates: []core.Template{templateFixture("nested-app", "app")}}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), noDirectApp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates", "nested-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	config, err = core.GoReleaserConfig(root)
	if err != nil || len(config) != 0 {
		t.Fatalf("GoReleaserConfig(no direct app) = %q, %v; want no artifacts", config, err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/mixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err = core.GoReleaserConfig(root)
	if err != nil || !strings.Contains(string(config), "binary: premise-cargo") {
		t.Fatalf("GoReleaserConfig(Go and Cargo library only) = %q, %v", config, err)
	}
}
