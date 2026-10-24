package backends

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

// Tests ----------------------------------------------------------------------

func TestReleaseConfiguration(t *testing.T) {
	t.Run("when releasing the root Go CLI", func(t *testing.T) {
		root := releaseConfigurationFixture(t, []string{"go"}, false)

		config, err := core.GoReleaserConfig(root)

		t.Run("it preserves the existing binary and archive names", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"project_name: workspace", "binary: workspace", "{{ .ProjectName }}-{{ .Tag }}-", "mode: keep-existing", "replace_existing_artifacts: true"} {
				if !strings.Contains(string(config), want) {
					t.Errorf("release configuration omits %q", want)
				}
			}
		})
	})
	t.Run("when releasing Cargo applications", func(t *testing.T) {
		root := releaseConfigurationFixture(t, []string{"cargo"}, false)

		config, err := core.GoReleaserConfig(root)

		t.Run("it includes application builds and omits library builds", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"id: rust-app", "id: rust-cli", "builder: rust", "x86_64-unknown-linux-gnu", "aarch64-apple-darwin"} {
				if !strings.Contains(string(config), want) {
					t.Errorf("release configuration omits %q", want)
				}
			}
			if strings.Contains(string(config), "id: rust-lib") || strings.Contains(string(config), "id: tauri-app") {
				t.Fatal("library produces an executable")
			}
			if count := strings.Count(string(config), "builder: rust"); count != 2 {
				t.Fatalf("expected two Cargo application builds, got %d", count)
			}
		})
	})
	t.Run("when releasing a mixed Go Cargo and Bun workspace", func(t *testing.T) {
		root := releaseConfigurationFixture(t, []string{"go", "cargo", "bun"}, false)

		config, err := core.GoReleaserConfig(root)

		t.Run("it combines the selected providers in one release", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"main: .", "builder: rust", "id: rust-app", "{{ .ProjectName }}-{{ .Tag }}-"} {
				if !strings.Contains(string(config), want) {
					t.Errorf("combined release omits %q", want)
				}
			}
		})
	})
	t.Run("when releasing only libraries", func(t *testing.T) {
		root := releaseConfigurationFixture(t, []string{"go", "cargo", "bun"}, true)

		config, err := core.GoReleaserConfig(root)

		t.Run("it creates a source release without executable builds", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(config), "skip: true") || !strings.Contains(string(config), "disable: true") || strings.Contains(string(config), "binary:") {
				t.Fatalf("expected a source-only release, got %s", config)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func releaseConfigurationFixture(t *testing.T, managers []string, librariesOnly bool) string {
	t.Helper()
	registerBuiltins(t)
	root := t.TempDir()
	manifest := core.NewManifest("workspace")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	manifest.TemplateRegistry = &core.TemplateRegistry{WorkspaceFiles: []string{}, Templates: []core.Template{}}
	manifest.Workspace.PackageManagers = managers
	for _, manager := range managers {
		switch manager {
		case "go":
			writePackageFixtureFile(t, root, "go.mod", "module example.com/repo\ngo 1.25.0\n")
			if !librariesOnly {
				writePackageFixtureFile(t, root, "main.go", "package main\nfunc main() {}\n")
			}
		case "cargo":
			writePackageFixtureFile(t, root, "Cargo.toml", "[workspace]\n")
			names := []string{"rust-lib"}
			if !librariesOnly {
				names = append(names, "rust-app", "rust-cli", "tauri-app")
			}
			for _, name := range names {
				kind := "app"
				if name == "rust-lib" {
					kind = "lib"
				}
				relative := "templates/" + name
				manifest.TemplateRegistry.Templates = append(manifest.TemplateRegistry.Templates, core.Template{Name: name, Kind: kind, Path: relative, Questions: []core.Question{{Prompt: "Name:", Type: "string", Populate: "name"}}})
				if name == "tauri-app" {
					writePackageFixtureFile(t, root, relative+"/src-tauri/Cargo.toml", "[package]\nname = \"tauri-app\"\nversion = \"0.1.0\"\n")
					continue
				}
				writePackageFixtureFile(t, root, relative+"/Cargo.toml", "[package]\nname = \""+name+"\"\nversion = \"0.1.0\"\n")
			}
		case "bun":
			writePackageFixtureFile(t, root, "package.json", `{"name":"workspace","private":true}`)
			writePackageFixtureFile(t, root, "bunfig.toml", "")
		}
	}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	return root
}
