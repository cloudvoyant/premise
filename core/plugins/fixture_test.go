package plugins

import (
	"path/filepath"

	"github.com/cloudvoyant/premise/core"
)

type Template = core.Template
type TemplateRegistry = core.TemplateRegistry

const ManifestFilename = core.ManifestFilename

var NewManifest = core.NewManifest
var SaveManifest = core.SaveManifest

func templateFixture(name, kind string) core.Template {
	return core.Template{
		Name: name, Kind: kind,
		Path:      filepath.ToSlash(filepath.Join("templates", name)),
		Version:   "0.1.0",
		Questions: []core.Question{{Prompt: "Project name:", Type: "string", Populate: "name"}},
	}
}
