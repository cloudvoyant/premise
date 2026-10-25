package backends

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

// Fixtures -------------------------------------------------------------------

type Template = core.Template
type TemplateRegistry = core.TemplateRegistry

const ManifestFilename = core.ManifestFilename

var NewManifest = core.NewManifest
var SaveManifest = core.SaveManifest

var testRegisterBuiltinsOnce sync.Once
var testRegisterBuiltinsErr error

// Private helpers ------------------------------------------------------------

func registerBuiltins(t *testing.T) {
	t.Helper()
	testRegisterBuiltinsOnce.Do(func() {
		for _, plugin := range []core.PackageManagerBackend{Go{}, Cargo{}, Bun{}} {
			if err := core.RegisterPackageManagerBackend(plugin); err != nil {
				testRegisterBuiltinsErr = err
				return
			}
		}
	})
	if testRegisterBuiltinsErr != nil {
		t.Fatal(testRegisterBuiltinsErr)
	}
}

func templateFixture(name, kind string) core.Template {
	return core.Template{
		Name: name, Kind: kind,
		Path:      filepath.ToSlash(filepath.Join("templates", name)),
		Version:   "0.1.0",
		Questions: []core.Question{{Prompt: "Project name:", Type: "string", Populate: "name"}},
	}
}

func writePackageFixtureFile(t *testing.T, root, path, text string) {
	t.Helper()
	destination := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func generationTestContext(t *testing.T, root, stage, project string, paths []string) core.PackageGenerationContext {
	t.Helper()
	values := map[string]core.PathValue{}
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		values[path] = core.PathValue{Present: true, Kind: "file", Mode: 0644, Data: data}
	}
	return core.PackageGenerationContext{WorkspaceRoot: root, WorkspaceName: "client", ProjectPath: project, Stage: stage, ClientFiles: values, ResolveConflict: core.MergeDecisions{}.Resolve}
}

func generationOutput(t *testing.T, plan *core.GeneratePlan, path string) []byte {
	t.Helper()
	for _, entry := range plan.Entries {
		if entry.Path == path {
			return entry.Output.Data
		}
	}
	t.Fatalf("no planned output for %s", path)
	return nil
}

// Cancelled application closes the public plan without executing native tools or publishing.
func discardGenerationPlan(t *testing.T, plan *core.GeneratePlan, root, project string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := core.ApplyGeneratePlan(ctx, plan, io.Discard); err == nil {
			t.Error("cancelled generation unexpectedly succeeded")
		}
		if _, err := os.Stat(filepath.Join(root, project)); !os.IsNotExist(err) {
			t.Errorf("cancelled generation published a project: %v", err)
		}
		stages, err := filepath.Glob(filepath.Join(root, filepath.Dir(project), ".premise-stage-*"))
		if err != nil || len(stages) != 0 {
			t.Errorf("generation stages remain: %v %v", stages, err)
		}
	})
}

func readPackageManifestFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "package-generation", "manifests", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func releaseConfigurationFixture(t *testing.T, managers []string, librariesOnly bool) string {
	t.Helper()
	registerBuiltins(t)
	root := t.TempDir()
	manifest := core.NewManifest("workspace")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	manifest.TemplateRegistry = &core.TemplateRegistry{
		WorkspaceFiles: []string{},
		Templates:      []core.Template{},
	}
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
				manifest.TemplateRegistry.Templates = append(manifest.TemplateRegistry.Templates, core.Template{
					Name: name,
					Kind: kind,
					Path: relative,
					Questions: []core.Question{
						{
							Prompt:   "Name:",
							Type:     "string",
							Populate: "name",
						},
					},
				})
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
