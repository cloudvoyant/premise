package core

import (
	"strings"
	"testing"
)

func TestTemplateRegistryRequiresExplicitWorkspaceFiles(t *testing.T) {
	manifest := NewManifest("registry")
	manifest.Workspace.Kind = ProjectKindTemplateRegistry
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "requires template_registry") {
		t.Fatalf("missing template_registry error = %v", err)
	}

	manifest.TemplateRegistry = &TemplateRegistry{Templates: []Template{}}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "template_registry.workspace_files is required") {
		t.Fatalf("Validate() error = %v", err)
	}

	manifest.TemplateRegistry.WorkspaceFiles = []string{}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("explicit empty workspace_files: %v", err)
	}

	manifest.TemplateRegistry.WorkspaceFiles = []string{"mise.toml", "mise.toml"}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate pattern") {
		t.Fatalf("duplicate workspace_files error = %v", err)
	}
}

func TestTemplateRegistryRequiresExplicitSafeTemplatePaths(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "", want: "path is required"},
		{path: ".", want: "below the registry root"},
		{path: "../template", want: "below the registry root"},
		{path: "templates/../template", want: "normalized"},
		{path: `/absolute/template`, want: "relative to the registry root"},
		{path: `templates\\app`, want: "forward slashes"},
	} {
		t.Run(test.path, func(t *testing.T) {
			template := templateFixture("app", "app")
			template.Path = test.path
			manifest := NewManifest("registry")
			manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{template}}
			if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}

	first := templateFixture("app", "app")
	second := templateFixture("other", "app")
	second.Path = first.Path
	manifest := NewManifest("registry")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{first, second}}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate template path") {
		t.Fatalf("Validate() duplicate path error = %v", err)
	}
}

func TestTemplateRegistryWorkspaceFilePatternsStayAtRoot(t *testing.T) {
	for _, test := range []struct {
		pattern string
		want    string
	}{
		{pattern: "", want: "pattern is required"},
		{pattern: "config/*.toml", want: "direct files"},
		{pattern: `config\\*.toml`, want: "direct files"},
		{pattern: "../mise.toml", want: "direct files"},
		{pattern: "[", want: "invalid glob"},
		{pattern: ManifestFilename, want: "registry metadata"},
	} {
		t.Run(test.pattern, func(t *testing.T) {
			manifest := NewManifest("registry")
			manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{test.pattern}, Templates: []Template{}}
			if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}
