package e2e_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
)

type fixedQuestionnaire map[string]string

func (answers fixedQuestionnaire) Ask(_ []core.Question) (map[string]string, error) {
	copy := make(map[string]string, len(answers))
	for name, value := range answers {
		copy[name] = value
	}
	return copy, nil
}

func workspaceMiseTemplate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "templates", "mise.toml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workspace mise template: %v", err)
	}
	return string(content)
}

func TestWorkspaceTemplateAndGenerationWorkflow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	workspaceTemplate := workspaceMiseTemplate(t)
	manifestPath, err := core.InitializeWorkspace(root, workspaceTemplate)
	if err != nil {
		t.Fatal(err)
	}
	workspaceMise, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(workspaceMise) != workspaceTemplate {
		t.Fatalf("workspace mise.toml does not match the canonical template:\n%s", workspaceMise)
	}
	if _, err := core.InitializeWorkspace(root, workspaceTemplate); err == nil {
		t.Fatal("expected existing manifest error")
	}
	templatePath, err := core.InitializeTemplate(root, "app")
	if err != nil {
		t.Fatal(err)
	}
	misePath := filepath.Join(templatePath, "mise.toml")
	if err := os.Chmod(misePath, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := []byte{'p', 'r', 'e', 'm', 'i', 's', 'e', '-', 'a', 'p', 'p', 0, 1}
	if err := os.WriteFile(filepath.Join(templatePath, "asset.bin"), binary, 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("mise.toml", filepath.Join(templatePath, "mise-link.toml")); err != nil {
			t.Fatal(err)
		}
	}

	registry, err := core.LoadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if names := registry.Names(); len(names) != 1 || names[0] != "app" {
		t.Fatalf("unexpected registry names: %v", names)
	}
	var output bytes.Buffer
	if err := core.Generate(context.Background(), root, ".:app", fixedQuestionnaire{"name": "orders"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Generated app orders") {
		t.Fatalf("unexpected generation output: %s", output.String())
	}
	destination := filepath.Join(root, "apps", "orders")
	source, err := core.TemplateDirectory(root, "app")
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Scaffold(core.ScaffoldRequest{Source: source, Destination: destination}); err == nil {
		t.Fatal("expected existing destination error")
	}

	generated, err := os.ReadFile(filepath.Join(destination, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "orders: build") || strings.Contains(string(generated), "premise-app") {
		t.Fatalf("unexpected generated template:\n%s", generated)
	}
	copiedBinary, err := os.ReadFile(filepath.Join(destination, "asset.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copiedBinary, binary) {
		t.Fatalf("binary changed: %v", copiedBinary)
	}
	info, err := os.Stat(filepath.Join(destination, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
	if runtime.GOOS != "windows" {
		link, err := os.Readlink(filepath.Join(destination, "mise-link.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if link != "mise.toml" {
			t.Fatalf("link = %q", link)
		}
	}

	reloaded, err := core.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Workspace.Projects) != 1 || reloaded.Workspace.Projects[0].Path != "apps/orders" {
		t.Fatalf("unexpected projects: %#v", reloaded.Workspace.Projects)
	}
}

func TestGenerateExplainsMissingTemplateManifest(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := core.InitializeWorkspace(workspace, workspaceMiseTemplate(t)); err != nil {
		t.Fatal(err)
	}

	templateSource := filepath.Join(t.TempDir(), "not-a-template")
	if err := os.MkdirAll(templateSource, 0o755); err != nil {
		t.Fatal(err)
	}
	err := core.Generate(context.Background(), workspace, templateSource+":premise-lib", fixedQuestionnaire{}, io.Discard)
	if err == nil {
		t.Fatal("expected missing template manifest error")
	}
	want := "template source \"" + templateSource + "\" does not contain premise.yaml"
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "choose a Premise template repository or run `pm init` in that directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitializeWorkspacePreservesExistingMiseConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	const existing = "[tools]\nnode = \"lts\"\n"
	misePath := filepath.Join(root, "mise.toml")
	if err := os.WriteFile(misePath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := core.InitializeWorkspace(root, workspaceMiseTemplate(t)); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(misePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != existing {
		t.Fatalf("existing mise.toml changed:\n%s", content)
	}
}

func TestManifestRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), core.ManifestFilename)
	content := `workspace:
  name: example
  schema-version: "0.1"
  providers:
    ci: github
    tools: mise
    tasks: mise
    infra: pulumi
    versioning: svu
  projects: []
unknown: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadManifest(path); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("expected strict YAML error, got %v", err)
	}
}

func TestConfigRejectsDuplicateProjects(t *testing.T) {
	config := core.NewManifest("example")
	config.Workspace.Projects = []core.Project{
		{Name: "orders", Template: ".:app", Path: "apps/orders"},
		{Name: "orders", Template: ".:app", Path: "apps/orders-v2"},
	}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate project name") {
		t.Fatalf("expected duplicate project name error, got %v", err)
	}

	config.Workspace.Projects[1] = core.Project{Name: "payments", Template: ".:app", Path: "apps/orders"}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate project path") {
		t.Fatalf("expected duplicate project path error, got %v", err)
	}
}

func TestScaffoldRejectsEscapingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires additional Windows privileges")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../secret", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	err := core.Scaffold(core.ScaffoldRequest{Source: source, Destination: filepath.Join(root, "destination")})
	if err == nil || !strings.Contains(err.Error(), "outside the template") {
		t.Fatalf("expected unsafe symlink error, got %v", err)
	}
}

func TestTemplateContractsCollectAllFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell fixture")
	}
	root := t.TempDir()
	for _, name := range []string{"app", "lib"} {
		if err := os.MkdirAll(filepath.Join(root, "templates", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	manifest := core.NewManifest("example")
	manifest.Templates = []core.Template{
		{Name: "app", Kind: "app", Questions: []core.Question{{Prompt: "App name:", Type: "string", Populate: "name"}}},
		{Name: "lib", Kind: "lib", Questions: []core.Question{{Prompt: "Library name:", Type: "string", Populate: "name"}}},
	}
	var output bytes.Buffer
	err := core.TestTemplateContracts(context.Background(), root, manifest, &output, io.Discard)
	if err == nil {
		t.Fatal("expected contract failures")
	}
	for _, expected := range []string{"template app task build failed", "template app task e2e failed", "template lib task publish:rc failed", "template lib task publish failed"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("combined error does not contain %q: %v", expected, err)
		}
	}
}
