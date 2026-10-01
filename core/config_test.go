package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInitializeWorkspaceWorkflowCollisionDoesNotWriteScaffold(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	collision := filepath.Join(root, ".github", "workflows", "ci.yml")
	if err := os.WriteFile(collision, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := InitializeWorkspace(root, "monorepo_root = true\n", WorkflowAsset{Path: ".github/workflows/ci.yml", Content: "replace me"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
	for _, path := range []string{ManifestFilename, "apps", "libs", "mise.toml"} {
		if _, statErr := os.Lstat(filepath.Join(root, path)); statErr == nil {
			t.Fatalf("scaffold wrote %s", path)
		}
	}
	got, readErr := os.ReadFile(collision)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "keep me" {
		t.Fatalf("collision content changed: %q", got)
	}
}

func TestInitializeWorkspaceRejectsSymlinkedWorkflowParent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	external := filepath.Join(t.TempDir(), "external")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, ".github")); err != nil {
		t.Fatal(err)
	}

	_, err := InitializeWorkspace(root, "monorepo_root = true\n", WorkflowAsset{Path: ".github/workflows/ci.yml", Content: "must not write"})
	if err == nil || !strings.Contains(err.Error(), "contains a symlink") {
		t.Fatalf("error = %v", err)
	}
	for _, path := range []string{ManifestFilename, "apps", "libs", "mise.toml"} {
		if _, statErr := os.Lstat(filepath.Join(root, path)); statErr == nil {
			t.Fatalf("scaffold wrote %s", path)
		}
	}
	if _, statErr := os.Lstat(filepath.Join(external, "workflows", "ci.yml")); statErr == nil {
		t.Fatal("workflow was written outside workspace")
	}
}

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
			// TODO: Validate the rejected path behavior, not only the error message.
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

func TestPublicationTargetsChooseConfiguredWorkspaceKind(t *testing.T) {
	registry := NewManifest("registry")
	registry.Workspace.Kind = ProjectKindTemplateRegistry
	registry.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{
		templateFixture("z", "app"), templateFixture("a", "lib"),
	}}
	registry.TemplateRegistry.Templates[0].Path = "templates/z"
	registry.TemplateRegistry.Templates[1].Path = "templates/a"
	registry.Workspace.Projects = []Project{{Name: "project", Template: "ignored", Path: "apps/project"}}
	got := registry.PublicationTargets()
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "z" {
		t.Fatalf("registry targets = %#v, want sorted declared templates", got)
	}
	if got := registry.DeclaredTemplates(); len(got) != 2 || got[0].Name != "z" {
		t.Fatalf("DeclaredTemplates changed ordering or semantics: %#v", got)
	}

	monorepo := NewManifest("workspace")
	monorepo.Workspace.Kind = ProjectKindMonorepo
	monorepo.TemplateRegistry = registry.TemplateRegistry
	monorepo.Workspace.Projects = []Project{
		{Name: "z", Template: "z", Version: "2", Path: "apps/z"},
		{Name: "a", Template: "a", Version: "1", Path: "libs/a"},
	}
	got = monorepo.PublicationTargets()
	want := []Template{{Name: "z", Kind: "app", Path: "apps/z", Version: "2"}, {Name: "a", Kind: "lib", Path: "libs/a", Version: "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("monorepo targets = %#v, want %#v", got, want)
	}
}

func TestPublicationTargetsEmptyAndHybridDeterministic(t *testing.T) {
	manifest := NewManifest("workspace")
	if got := manifest.PublicationTargets(); got != nil && len(got) != 0 {
		t.Fatalf("empty targets = %#v, want empty", got)
	}
	manifest.Workspace.Kind = ProjectKindMonorepo
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{templateFixture("registry", "app")}}
	manifest.Workspace.Projects = []Project{
		{Name: "b", Template: "b", Path: "libs/b"},
		{Name: "a", Template: "a", Path: "apps/a"},
	}
	got := manifest.PublicationTargets()
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("hybrid targets = %#v, want sorted projects only", got)
	}
}

func TestWorkspacePackageManagersMergeInFirstSeenOrder(t *testing.T) {
	manifest := NewManifest("workspace")
	manifest.Workspace.PackageManagers = []string{"go"}
	if err := manifest.MergePackageManagers("bun", "go", "cargo", "bun"); err != nil {
		t.Fatal(err)
	}
	want := []string{"go", "bun", "cargo"}
	if got := manifest.Workspace.PackageManagers; !reflect.DeepEqual(got, want) {
		t.Fatalf("package managers = %#v, want %#v", got, want)
	}
}

func TestWorkspacePackageManagersRejectDuplicates(t *testing.T) {
	manifest := NewManifest("workspace")
	manifest.Workspace.PackageManagers = []string{"bun", "bun"}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Validate() = %v; want duplicate package manager error", err)
	}
	manifest.Workspace.PackageManagers = []string{"bun", "cargo"}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate() = %v; distinct package managers should be allowed", err)
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
