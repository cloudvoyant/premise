package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

// Public API -----------------------------------------------------------------

func TestWorkspaceWithoutInfrastructureDoesNotAddPulumi(t *testing.T) {
	// Arrange.
	binary := premiseBinary(t)
	root := t.TempDir()

	// Act.
	infraCommand(t, root, binary, "init")

	// Assert.
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Workspace.Infra != nil || strings.Contains(string(data), "pulumi") {
		t.Fatal("workspace without infrastructure acquired a Pulumi dependency")
	}
	if _, err := os.Stat(filepath.Join(root, "infra")); !os.IsNotExist(err) {
		t.Fatal("workspace without infrastructure acquired an infra directory")
	}
}

func TestLegacyInfrastructureBindingsFailBeforeAnyPulumiOperation(t *testing.T) {
	// Arrange.
	binary := premiseBinary(t)
	root := t.TempDir()
	copyInfraFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/legacy-infra"), root)

	// Act.
	command := exec.Command(binary, "infra", "plan", "--env", "dev")
	command.Dir = root
	output, err := command.CombinedOutput()

	// Assert.
	if err == nil || !strings.Contains(string(output), "field bindings not found") {
		t.Fatalf("legacy binding contract was silently accepted: %v\n%s", err, output)
	}
}

func TestInfrastructureLanguageSetupPreservesRuntimeAndUsesPinnedPulumi(t *testing.T) {
	if os.Getenv("PREMISE_TEST_PULUMI") != "1" {
		t.Skip("set PREMISE_TEST_PULUMI=1 to initialize real Go, TypeScript, and Python projects")
	}
	binary := premiseBinary(t)
	for _, language := range []string{"go", "typescript", "python"} {
		t.Run(language, func(t *testing.T) {
			// Arrange.
			t.Setenv("PULUMI_HOME", t.TempDir())
			root := filepath.Join(t.TempDir(), "client")
			if err := os.MkdirAll(root, 0755); err != nil {
				t.Fatal(err)
			}

			// Act.
			infraCommand(t, root, binary, "init", "--infra", "--infra-language", language, "--pulumi-version", "3.268.0")
			configureLocalInfrastructure(t, root)
			infraCommand(t, root, "mise", "run", "infra:install")
			infraCommand(t, root, binary, "infra", "init", "--env", "preview", "--branch", "feature/language")
			infraCommand(t, root, binary, "infra", "plan", "--env", "preview", "--branch", "feature/language")

			// Assert.
			metadata, err := os.ReadFile(filepath.Join(root, "infra/Pulumi.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			runtime := language
			if language == "typescript" {
				runtime = "nodejs"
			}
			if !strings.Contains(string(metadata), runtime) {
				t.Fatalf("%s starter has wrong runtime:\n%s", language, metadata)
			}
			mise, err := os.ReadFile(filepath.Join(root, "mise.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(mise), "pulumi = '3.268.0'") {
				t.Fatalf("Pulumi version was not persisted:\n%s", mise)
			}
			if _, err := os.Stat(filepath.Join(root, "infra/infra_test.go")); !os.IsNotExist(err) {
				t.Fatal("initialization generated an infrastructure test")
			}
			infraCommand(t, root, binary, "infra", "destroy", "--env", "preview", "--branch", "feature/language")
		})
	}
}

func TestInfrastructureAdoptionPreservesExistingFilesAndToolPins(t *testing.T) {
	// Arrange.
	binary := premiseBinary(t)
	root := filepath.Join(t.TempDir(), "client")
	if err := os.MkdirAll(filepath.Join(root, "infra"), 0755); err != nil {
		t.Fatal(err)
	}
	original := "name: client\nruntime: nodejs\n"
	if err := os.WriteFile(filepath.Join(root, "infra/Pulumi.yaml"), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "infra/index.js"), []byte("exports.marker = 'client-owned';\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("monorepo_root = true\n[tools]\npulumi = '3.200.0'\nnode = '22'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Act.
	infraCommand(t, root, binary, "init", "--infra", "--infra-language", "javascript")

	// Assert.
	actual, err := os.ReadFile(filepath.Join(root, "infra/Pulumi.yaml"))
	if err != nil || string(actual) != original {
		t.Fatalf("existing Pulumi metadata changed: %v\n%s", err, actual)
	}
	mise, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mise), "pulumi = '3.200.0'") || !strings.Contains(string(mise), "node = '22'") {
		t.Fatalf("existing tool pins changed:\n%s", mise)
	}
}

func TestConflictingInfrastructureLanguageLeavesWorkspaceUnchanged(t *testing.T) {
	// Arrange.
	binary := premiseBinary(t)
	root := filepath.Join(t.TempDir(), "client")
	if err := os.MkdirAll(filepath.Join(root, "infra"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "infra/Pulumi.yaml"), []byte("name: client\nruntime: python\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Act.
	command := exec.Command(binary, "init", "--infra", "--infra-language", "go")
	command.Dir = root
	output, err := command.CombinedOutput()

	// Assert.
	if err == nil || !strings.Contains(string(output), "would change the existing program") {
		t.Fatalf("conflicting language accepted: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, core.ManifestFilename)); !os.IsNotExist(err) {
		t.Fatal("failed setup created a workspace manifest")
	}
	if _, err := os.Stat(filepath.Join(root, "mise.toml")); !os.IsNotExist(err) {
		t.Fatal("failed setup created a Mise configuration")
	}
}

// Private helpers ------------------------------------------------------------

func configureLocalInfrastructure(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, core.ManifestFilename)
	manifest, err := core.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Infra.Backend = "local"
	manifest.Workspace.Infra.Backends = map[string]core.InfraBackend{"local": {URL: "file://" + t.TempDir(), AllowLocal: true}}
	if err := core.SaveManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
}
