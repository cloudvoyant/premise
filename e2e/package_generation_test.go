package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

// Tests ----------------------------------------------------------------------

func testNativeWorkspaceGeneration(t *testing.T) {
	binary := premiseBinary(t)
	for _, manager := range []string{"go", "cargo", "bun"} {
		t.Run("when generating a "+manager+" workspace", func(t *testing.T) {
			base := t.TempDir()
			workspace := filepath.Join(base, "client")
			if err := os.MkdirAll(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			registry := nativeRegistry(t, manager, filepath.Join(base, "registry"))
			runCommand(t, workspace, binary, "init")

			runCommand(t, workspace, binary, "generate", registry+":lib-template", "--answer", "name=helper,library=helper")
			runCommand(t, workspace, binary, "generate", registry+":app-template", "--answer", "name=tool,library=helper")
			if manager == "cargo" || manager == "bun" {
				lock := "Cargo.lock"
				if manager == "bun" {
					lock = "bun.lock"
				}
				if _, err := os.Stat(filepath.Join(workspace, lock)); err != nil {
					t.Fatalf("generation discarded candidate lockfile: %v", err)
				}
			}
			runCommand(t, workspace, binary, "install")

			t.Run("it resolves the root and uses its existing sibling", func(t *testing.T) {
				output := runCommand(t, workspace, binary, "run", "tool:run")
				if strings.TrimSpace(output) != "hello" {
					t.Fatalf("app output = %q", output)
				}
				switch manager {
				case "go":
					runCommand(t, workspace, "go", "test", "./...")
					assertAbsent(t, filepath.Join(workspace, "libs/helper/go.mod"))
					assertAbsent(t, filepath.Join(workspace, "apps/tool/go.mod"))
					assertAbsent(t, filepath.Join(workspace, "go.work"))
				case "cargo":
					output := runCommand(t, workspace, "cargo", "metadata", "--no-deps", "--format-version=1", "--offline")
					var metadata struct {
						WorkspaceMembers []string `json:"workspace_members"`
					}
					if err := json.Unmarshal([]byte(output), &metadata); err != nil {
						t.Fatal(err)
					}
					if len(metadata.WorkspaceMembers) != 2 {
						t.Fatalf("Cargo members = %v", metadata.WorkspaceMembers)
					}
					runCommand(t, workspace, "cargo", "fetch", "--offline")
					if _, err := os.Stat(filepath.Join(workspace, "Cargo.lock")); err != nil {
						t.Fatal(err)
					}
				case "bun":
					// Generation itself must retain the lock produced inside its candidate.
					if _, err := os.Stat(filepath.Join(workspace, "bun.lock")); err != nil {
						t.Fatal(err)
					}
					runCommand(t, workspace, "bun", "install", "--ignore-scripts", "--frozen-lockfile")
					root := readFile(t, filepath.Join(workspace, "package.json"))
					if strings.Contains(root, "registry-development") || strings.Contains(root, "templates/") {
						t.Fatalf("registry root leaked: %s", root)
					}
				}
				manifest, err := core.LoadManifest(filepath.Join(workspace, core.ManifestFilename))
				if err != nil || len(manifest.Workspace.Projects) != 2 {
					t.Fatalf("project provenance missing: %#v %v", manifest, err)
				}
				assertNoGenerationTemporaryDirectories(t, filepath.Join(workspace, "apps"), filepath.Join(workspace, "libs"))
			})
		})
	}
}

func testMixedNativeWorkspaceGeneration(t *testing.T) {
	binary := premiseBinary(t)
	for _, order := range [][]string{{"bun", "cargo"}, {"cargo", "bun"}} {
		t.Run("when adding "+strings.Join(order, " then ")+" to Go", func(t *testing.T) {
			base := t.TempDir()
			workspace := filepath.Join(base, "client")
			if err := os.MkdirAll(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			runCommand(t, workspace, binary, "init")
			managers := append([]string{"go"}, order...)

			for _, manager := range managers {
				registry := nativeRegistry(t, manager, filepath.Join(base, "registry-"+manager))
				name := manager + "-helper"
				runCommand(t, workspace, binary, "generate", registry+":lib-template", "--answer", "name="+name+",library="+name)
				runCommand(t, workspace, binary, "generate", registry+":app-template", "--answer", "name="+manager+"-tool,library="+name)
			}

			runCommand(t, workspace, binary, "install")

			t.Run("it keeps all native workspaces usable", func(t *testing.T) {
				for _, manager := range managers {
					output := runCommand(t, workspace, binary, "run", manager+"-tool:run")
					if strings.TrimSpace(output) != "hello" {
						t.Fatalf("%s output: %q", manager, output)
					}
				}
				assertFileContains(t, filepath.Join(workspace, "Cargo.toml"), "libs/cargo-helper")
				assertFileContains(t, filepath.Join(workspace, "go.mod"), "module example.com/workspace")
				assertAbsent(t, filepath.Join(workspace, "libs/go-helper/go.mod"))
			})
			t.Run("it validates a language neutral template without touching native manifests", func(t *testing.T) {
				registry := filepath.Join(base, "generic")
				manifest := core.NewManifest("generic")
				manifest.TemplateRegistry = &core.TemplateRegistry{WorkspaceFiles: []string{}, Templates: []core.Template{{Name: "notes", Kind: "lib", Path: "templates/notes", Questions: []core.Question{{Prompt: "Name:", Type: "string", Populate: "name"}}}}}
				if err := core.SaveManifest(filepath.Join(registry, core.ManifestFilename), manifest); err != nil {
					t.Fatal(err)
				}
				project := filepath.Join(registry, "templates/notes")
				copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/package-generation/generic"), project)
				originals := map[string]string{}
				for _, path := range []string{"go.mod", "Cargo.toml", "package.json"} {
					originals[path] = readFile(t, filepath.Join(workspace, path))
				}

				runCommand(t, workspace, binary, "generate", registry+":notes", "--answer", "name=notes")

				for path, before := range originals {
					assertFileContent(t, filepath.Join(workspace, path), before)
				}
				assertFileContains(t, filepath.Join(workspace, "libs/notes/README.md"), "generic fixture")
			})
		})
	}
}

func testExistingStandaloneCargoGeneration(t *testing.T) {
	binary := premiseBinary(t)
	base := t.TempDir()
	workspace := filepath.Join(base, "client")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	runCommand(t, workspace, binary, "init")
	old := filepath.Join(workspace, "libs/old-helper")
	copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/package-generation/cargo/lib"), old)
	path := filepath.Join(old, "Cargo.toml")
	writeFixtureFile(t, path, strings.ReplaceAll(readFile(t, path), "lib-template", "old-helper"), 0644)
	manifestPath := filepath.Join(workspace, core.ManifestFilename)
	manifest, err := core.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.PackageManagers = []string{"cargo"}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, core.Project{Name: "old-helper", Template: "legacy:lib", Path: "libs/old-helper"})
	if err := core.SaveManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	registry := nativeRegistry(t, "cargo", filepath.Join(base, "registry"))

	runCommand(t, workspace, binary, "generate", registry+":lib-template", "--answer", "name=new-helper,library=new-helper")
	runCommand(t, workspace, binary, "install")

	t.Run("it keeps existing declared crates in the new root workspace", func(t *testing.T) {
		metadata := runCommand(t, workspace, "cargo", "metadata", "--no-deps", "--format-version=1", "--offline")
		var result struct {
			WorkspaceMembers []string `json:"workspace_members"`
		}
		if err := json.Unmarshal([]byte(metadata), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.WorkspaceMembers) != 2 {
			t.Fatalf("existing crate lost during root creation: %s", metadata)
		}
		assertFileContains(t, filepath.Join(workspace, "Cargo.toml"), "libs/old-helper")
		assertFileContains(t, filepath.Join(workspace, "Cargo.toml"), "libs/new-helper")
		runCommand(t, old, "cargo", "test", "--offline")
	})
}

func testNativeGenerationConflicts(t *testing.T) {
	binary := premiseBinary(t)
	for _, manager := range []string{"go", "cargo", "bun"} {
		t.Run("when "+manager+" inputs conflict", func(t *testing.T) {
			base := t.TempDir()
			workspace := filepath.Join(base, "client")
			if err := os.MkdirAll(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			runCommand(t, workspace, binary, "init")
			registry := nativeRegistry(t, manager, filepath.Join(base, "registry"))
			path, original, expected := "go.mod", "module example.com/different\ngo 1.25.0\n", "workspace Go module"
			if manager == "cargo" {
				path, original, expected = "Cargo.toml", "[workspace]\nmembers=[]\nexclude=['libs/*']\nresolver='3'\n", "excludes generated crate"
			}
			if manager == "bun" {
				path, original, expected = "package.json", `{"name":"client","workspaces":["apps/*","!libs/*"]}`, "excludes generated project"
			}
			writeFixtureFile(t, filepath.Join(workspace, path), original, 0640)
			manifest := readFile(t, filepath.Join(workspace, core.ManifestFilename))

			output := commandFailure(t, workspace, binary, "generate", registry+":lib-template", "--answer", "name=helper,library=helper")

			if !strings.Contains(output, expected) {
				t.Fatalf("expected native conflict %q: %s", expected, output)
			}
			assertFileContent(t, filepath.Join(workspace, path), original)
			assertFileContent(t, filepath.Join(workspace, core.ManifestFilename), manifest)
			info, err := os.Stat(filepath.Join(workspace, path))
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatalf("root mode changed: %v %v", info, err)
			}
			assertAbsent(t, filepath.Join(workspace, "libs/helper"))
			assertNoGenerationTemporaryDirectories(t, filepath.Join(workspace, "libs"))
		})
	}
}

// Private helpers ------------------------------------------------------------

func nativeRegistry(t *testing.T, manager, root string) string {
	t.Helper()
	manifest := core.NewManifest("registry-development")
	manifest.Workspace.PackageManagers = []string{manager}
	manifest.TemplateRegistry = &core.TemplateRegistry{WorkspaceFiles: []string{}, Templates: []core.Template{}}
	for _, kind := range []string{"lib", "app"} {
		name := kind + "-template"
		manifest.TemplateRegistry.Templates = append(manifest.TemplateRegistry.Templates, core.Template{
			Name: name, Kind: kind, Path: "templates/" + name, Version: "0.1.0",
			Questions:     []core.Question{{Prompt: "Name:", Type: "string", Populate: "name"}, {Prompt: "Sibling name:", Type: "string", Populate: "library"}},
			Substitutions: map[string]string{name: "name", "LIBRARY_NAME": "library"},
		})
		copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/package-generation", manager, kind), filepath.Join(root, "templates", name))
	}
	if manager == "bun" {
		manifest.TemplateRegistry.WorkspaceFiles = []string{"package.json"}
		writeFixtureFile(t, filepath.Join(root, "package.json"), `{"name":"registry-development","private":true,"workspaces":["templates/*","apps/*","libs/*"]}`, 0644)
	}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	return root
}
