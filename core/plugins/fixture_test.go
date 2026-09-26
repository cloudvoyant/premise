package plugins

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

type Template = core.Template
type TemplateRegistry = core.TemplateRegistry

const ManifestFilename = core.ManifestFilename

var NewManifest = core.NewManifest
var SaveManifest = core.SaveManifest

var testRegisterBuiltinsOnce sync.Once
var testRegisterBuiltinsErr error

func registerBuiltins(t *testing.T) {
	t.Helper()
	testRegisterBuiltinsOnce.Do(func() {
		for _, plugin := range []core.PackageManagerPlugin{Go{}, Cargo{}, Bun{}} {
			if err := core.RegisterPackageManagerPlugin(plugin); err != nil {
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
