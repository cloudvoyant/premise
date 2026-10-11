package core

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestInfrastructureSelection(t *testing.T) {
	root, manifest := infrastructureFixture(t)
	if targets := manifest.PublicationTargets(); len(targets) != 1 || targets[0].Name != "site" {
		t.Fatalf("publication targets: %v", targets)
	}
	projects, err := selectInfraProjects(root, InfraOptions{All: true, Environment: "dev"})
	if err != nil || len(projects) != 2 || projects[0].Name != "shared" {
		t.Fatalf("shared first: %v, %v", projects, err)
	}
	for _, options := range []InfraOptions{{All: true}, {Environment: "../prod", All: true}, {Environment: "dev"}, {Environment: "dev", All: true, Project: "environment"}, {Environment: "shared", All: true}} {
		t.Run(fmt.Sprintf("%+v", options), func(t *testing.T) {
			if _, err := selectInfraProjects(root, options); err == nil {
				t.Fatalf("accepted invalid selection: %+v", options)
			}
		})
	}
}

func TestInfrastructureBindingProjectDefaults(t *testing.T) {
	root, _ := infrastructureFixture(t)
	settingsPath := filepath.Join(root, "infra/environment/Pulumi.yaml")
	originalStack := "encryptionsalt: preserved\nconfig:\n  secret:\n    secure: encrypted\n"
	writeInfrastructureFixture(t, root, "infra/environment/Pulumi.dev.yaml", originalStack)
	if err := BindInfra(root, "site", "environment", "dist", false); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"gcp:project:", "value: test-project", "site: ../../apps/site/dist"} {
		if !strings.Contains(readInfrastructureFixture(t, settingsPath), expected) {
			t.Fatalf("missing %q in project defaults", expected)
		}
	}
	if got := readInfrastructureFixture(t, filepath.Join(root, "infra/environment/Pulumi.dev.yaml")); got != originalStack {
		t.Fatalf("stack changed: %s", got)
	}
	before := readInfrastructureFixture(t, settingsPath)
	if err := BindInfra(root, "site", "environment", "dist", false); err != nil {
		t.Fatal(err)
	}
	if got := readInfrastructureFixture(t, settingsPath); got != before {
		t.Fatalf("repeat binding changed defaults: %s", got)
	}
}

func TestInfrastructureBindingRejectsInvalidChanges(t *testing.T) {
	for _, test := range []struct{ name, project, artifact string }{
		{"shared requires flag", "shared", "dist"}, {"path escape", "environment", "../outside"}, {"conflicting binding", "environment", "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := infrastructureFixture(t)
			if err := BindInfra(root, "site", "environment", "dist", false); err != nil {
				t.Fatal(err)
			}
			if err := BindInfra(root, "site", test.project, test.artifact, false); err == nil {
				t.Fatal("accepted invalid binding")
			}
		})
	}
}

func TestInfrastructureBindingSharedContract(t *testing.T) {
	root, _ := infrastructureFixture(t)
	if err := BindInfra(root, "site", "shared", "dist", true); err != nil {
		t.Fatal(err)
	}
	configuration := readInfrastructureFixture(t, filepath.Join(root, "infra/projects/shared/Pulumi.yaml"))
	for _, expected := range []string{"assets:", "../../../apps/site/dist"} {
		if !strings.Contains(configuration, expected) {
			t.Fatalf("missing %q: %s", expected, configuration)
		}
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range manifest.Workspace.Projects {
		if project.Name == "shared" && (project.Infra == nil || !project.Infra.Shared) {
			t.Fatal("shared scope not persisted under infra config")
		}
	}
}

func TestLocalPulumiBackendRejectsUnsupportedLocations(t *testing.T) {
	for _, backend := range []string{"gs://state", "file://relative"} {
		t.Run(backend, func(t *testing.T) {
			if _, err := localPulumiBackend(t.TempDir(), backend); err == nil {
				t.Fatalf("accepted %q", backend)
			}
		})
	}
}

func TestLocalTemplateCopyOmitsDependencySymlinks(t *testing.T) {
	source, destination := t.TempDir(), filepath.Join(t.TempDir(), "generated")
	dependencies := filepath.Join(source, "node_modules", ".bin")
	if err := os.MkdirAll(dependencies, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(dependencies, "tsc")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, destination, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "main.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("dependencies copied: %v", err)
	}
}

func TestStandardInfrastructureEnvironments(t *testing.T) {
	for _, env := range []string{"dev", "stage", "prod"} {
		stack, err := InfraStack(InfraOptions{Environment: env})
		if err != nil || stack != env {
			t.Fatalf("%s: %s, %v", env, stack, err)
		}
	}
	first, err := InfraStack(InfraOptions{Environment: "preview", Branch: "feature/a"})
	if err != nil {
		t.Fatal(err)
	}
	repeat, _ := InfraStack(InfraOptions{Environment: "preview", Branch: "feature/a"})
	other, _ := InfraStack(InfraOptions{Environment: "preview", Branch: "feature-a"})
	if first != repeat || first == other || !strings.HasPrefix(first, "preview-feature-a-") {
		t.Fatalf("preview names: %s %s %s", first, repeat, other)
	}
	for _, options := range []InfraOptions{{Environment: "preview"}, {Environment: "qa"}, {Environment: "prod", Branch: "main"}, {Shared: true, Environment: "preview", Branch: "a"}} {
		if _, err := InfraStack(options); err == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
}

func TestClientOwnedComponentRegistration(t *testing.T) {
	manifest := NewManifest("client")
	manifest.Workspace.Projects = []Project{{Name: "bucket-component", Path: "infra/components/bucket"}}
	root := t.TempDir()
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	projects, err := infrastructureProjects(root, "", true, true)
	if err != nil || len(projects) != 1 {
		t.Fatalf("test components: %v %v", projects, err)
	}
	programs, err := infrastructureProjects(root, "", true, false)
	if err != nil || len(programs) != 0 {
		t.Fatalf("component selected for deployment: %v %v", programs, err)
	}
}

func TestLocalPulumiBackendEscapesFilesystemPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "client #50%? space")
	backend, err := localPulumiBackend(root, "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(backend)
	if err != nil || parsed.Path != filepath.Join(root, ".pulumi-state") || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("backend %q: %v", backend, err)
	}
	if _, err := os.Stat(parsed.Path); err != nil {
		t.Fatal(err)
	}
}

func TestInfrastructureBindingRejectsInitializedScopeChange(t *testing.T) {
	root, _ := infrastructureFixture(t)
	writeInfrastructureFixture(t, root, "infra/environment/Pulumi.dev.yaml", "config: {}\n")
	projectPath := filepath.Join(root, "infra/environment/Pulumi.yaml")
	manifestPath := filepath.Join(root, ManifestFilename)
	beforeProject, beforeManifest := readInfrastructureFixture(t, projectPath), readInfrastructureFixture(t, manifestPath)
	if err := BindInfra(root, "site", "environment", "dist", true); err == nil {
		t.Fatal("accepted initialized scope change")
	}
	if readInfrastructureFixture(t, projectPath) != beforeProject || readInfrastructureFixture(t, manifestPath) != beforeManifest {
		t.Fatal("rejected scope change modified files")
	}
}

func TestInfraConfigRejectsApplicationProjects(t *testing.T) {
	manifest := NewManifest("client")
	manifest.Workspace.Projects = []Project{{Name: "site", Template: "registry:app", Path: "apps/site", Infra: &InfraProjectConfig{Shared: true}}}
	if err := manifest.Validate(); err == nil {
		t.Fatal("application accepted infra configuration")
	}
}

// Private helpers ------------------------------------------------------------

func infrastructureFixture(t *testing.T) (string, Config) {
	t.Helper()
	root := t.TempDir()
	manifest := NewManifest("client")
	manifest.Workspace.Projects = []Project{
		{Name: "environment", Template: "registry:bucket", Path: "infra/environment"},
		{Name: "site", Template: "registry:app", Path: "apps/site"},
		{Name: "shared", Template: "registry:bucket", Path: "infra/projects/shared"},
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	for _, project := range manifest.Workspace.Projects {
		if err := os.MkdirAll(filepath.Join(root, project.Path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeInfrastructureFixture(t, root, "infra/environment/Pulumi.yaml", "name: environment\nruntime: go\nconfig:\n  gcp:project:\n    value: test-project\n")
	writeInfrastructureFixture(t, root, "infra/environment/premise-infra.yaml", "artifact_config_key: environment:artifacts\n")
	writeInfrastructureFixture(t, root, "infra/projects/shared/Pulumi.yaml", "name: shared\nruntime: go\n")
	writeInfrastructureFixture(t, root, "infra/projects/shared/premise-infra.yaml", "artifact_config_key: assets\n")
	return root, manifest
}

func writeInfrastructureFixture(t *testing.T, root, path, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readInfrastructureFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
