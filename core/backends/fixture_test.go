package backends

import (
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
