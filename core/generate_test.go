package core

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixedQuestionnaire map[string]string

func (answers fixedQuestionnaire) Ask(_ []Question) (map[string]string, error) {
	response := make(map[string]string, len(answers))
	for name, value := range answers {
		response[name] = value
	}
	return response, nil
}

func fixedGenerateOptions(answers fixedQuestionnaire) GenerateOptions {
	return GenerateOptions{
		Questionnaire: answers,
		Resolver:      &mapMergeResolver{Decisions: map[string]MergeDecision{}},
	}
}

func TestGenerateRecordsQualifiedSelector(t *testing.T) {
	installMiseTestShim(t, false)
	cargoRegistry := writeRegistryFixture(t, templateFixture("premise-rust-lib", "lib"))
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := InitializeWorkspace(workspace, "[tasks.build]\nrun = 'echo ok'\n"); err != nil {
		t.Fatal(err)
	}

	const selector = "cloudvoyant/premise-cargo:premise-rust-lib"
	if err := Generate(context.Background(), workspace, selector, fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard); err != nil {
		t.Fatal(err)
	}

	manifest, err := LoadManifest(filepath.Join(workspace, ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Workspace.Projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(manifest.Workspace.Projects))
	}
	project := manifest.Workspace.Projects[0]
	if project.Template != selector {
		t.Fatalf("recorded template = %q, want %q", project.Template, selector)
	}
	if project.Path != "libs/orders" {
		t.Fatalf("recorded path = %q, want libs/orders", project.Path)
	}
	if _, err := os.Stat(filepath.Join(workspace, "libs", "orders")); err != nil {
		t.Fatalf("generated destination missing: %v", err)
	}
}

func TestGenerateMergesSharedFilesWithSelectedTemplate(t *testing.T) {
	installMiseTestShim(t, false)
	template := templateFixture("premise-rust-lib", "lib")
	template.Substitutions = map[string]string{"shared-placeholder": "name"}
	cargoRegistry := writeRegistryFixture(t, template)
	templatesRoot := filepath.Join(cargoRegistry, "templates")
	if err := os.WriteFile(filepath.Join(templatesRoot, ".shared-config"), []byte("name=shared-placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templatesRoot, "mise.toml"), []byte("[tasks.build]\nrun = 'echo shared'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := InitializeWorkspace(workspace, "[tasks.build]\nrun = 'echo ok'\n"); err != nil {
		t.Fatal(err)
	}

	const selector = "cloudvoyant/premise-cargo:premise-rust-lib"
	if err := Generate(context.Background(), workspace, selector, fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(workspace, "libs", "orders")
	shared, err := os.ReadFile(filepath.Join(destination, ".shared-config"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(shared), "name=orders\n"; got != want {
		t.Fatalf("shared file = %q, want %q", got, want)
	}
	overlay, err := os.ReadFile(filepath.Join(destination, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(overlay), "[tasks]\n[tasks.build]\nrun = ['echo shared', 'echo ok']\n"; got != want {
		t.Fatalf("merged mise.toml = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(destination, "premise-rust-lib")); err == nil {
		t.Fatal("template directory was copied as shared content")
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect unexpected shared directory: %v", err)
	}
}

func TestGeneratePrintsSortedCollisionStrategiesBeforeValidation(t *testing.T) {
	installMiseTestShim(t, false)
	template := templateFixture("app", "app")
	registry := writeRegistryFixture(t, template)
	shared := filepath.Join(registry, "templates")
	selected := filepath.Join(shared, "app")
	writeTestFile(t, filepath.Join(shared, ".gitattributes"), "*.txt text\n", 0o644)
	writeTestFile(t, filepath.Join(selected, ".gitattributes"), "*.sh text eol=lf\n", 0o644)
	writeTestFile(t, filepath.Join(shared, ".gitignore"), "shared/\n", 0o644)
	writeTestFile(t, filepath.Join(selected, ".gitignore"), "selected/\n", 0o644)
	writeTestFile(t, filepath.Join(shared, "NOTICE"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "NOTICE"), "selected\n", 0o644)
	writeTestFile(t, filepath.Join(shared, "mise.toml"), "[tools]\ngo = '1.25'\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\ngo = '1.24'\n", 0o644)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if _, err := InitializeWorkspace(workspace, "monorepo_root = true\n"); err != nil {
		t.Fatal(err)
	}
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{"NOTICE": {Choice: MergeChoiceKeepShared}}}
	var output bytes.Buffer
	if err := Generate(context.Background(), workspace, registry+":app", GenerateOptions{
		Questionnaire: fixedQuestionnaire{"name": "orders"},
		Resolver:      resolver,
	}, &output); err != nil {
		t.Fatal(err)
	}

	want := "Template root comparison:\n" +
		"- .gitattributes: smart line merge\n" +
		"- .gitignore: smart line merge\n" +
		"- NOTICE: whole-file decision\n" +
		"- mise.toml: semantic Mise merge\n"
	if !strings.HasPrefix(output.String(), want) {
		t.Fatalf("comparison output =\n%s\nwant prefix =\n%s", output.String(), want)
	}
}

func TestGenerateToolPreflightFailureLeavesDestinationAndManifestUntouched(t *testing.T) {
	installScopedMiseTestShim(t, "premise-tool-preflight-")
	registry := writeRegistryFixture(t, templateFixture("app", "app"))
	shared := filepath.Join(registry, "templates")
	selected := filepath.Join(shared, "app")
	writeTestFile(t, filepath.Join(shared, "mise.toml"), "[tools]\nbun = '1.2'\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nbun = '1.1'\n", 0o644)

	workspace := filepath.Join(t.TempDir(), "workspace")
	manifestPath, err := InitializeWorkspace(workspace, "monorepo_root = true\n")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(t.TempDir(), "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)

	err = Generate(context.Background(), workspace, registry+":app", fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "tool update bun 1.1 -> 1.2 failed") {
		t.Fatalf("error = %v", err)
	}
	assertGenerationFailureCleanup(t, workspace, filepath.Join("apps", "orders"), manifestPath, before, temp)
}

func TestGenerateCandidateFailureLeavesDestinationAndManifestUntouched(t *testing.T) {
	installScopedMiseTestShim(t, "premise-candidate-validation-")
	registry := writeRegistryFixture(t, templateFixture("app", "app"))
	workspace := filepath.Join(t.TempDir(), "workspace")
	manifestPath, err := InitializeWorkspace(workspace, "monorepo_root = true\n")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(t.TempDir(), "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)

	err = Generate(context.Background(), workspace, registry+":app", fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "generated candidate validation failed") {
		t.Fatalf("error = %v", err)
	}
	assertGenerationFailureCleanup(t, workspace, filepath.Join("apps", "orders"), manifestPath, before, temp)
}

func TestGenerateProjectRegistrationFailureRemovesCommittedDestination(t *testing.T) {
	installMiseTestShim(t, false)
	registry := writeRegistryFixture(t, templateFixture("app", "app"))
	workspace := filepath.Join(t.TempDir(), "workspace")
	manifestPath, err := InitializeWorkspace(workspace, "monorepo_root = true\n")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: "orders", Template: "old:app", Path: "apps/orders"})
	if err := SaveManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	err = Generate(context.Background(), workspace, registry+":app", fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "project \"orders\" is already registered") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "apps", "orders")); !os.IsNotExist(err) {
		t.Fatalf("destination exists after registration failure: %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("manifest changed after registration failure:\n%s", after)
	}
}

func installScopedMiseTestShim(t *testing.T, failDirectoryFragment string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "mise.log")
	script := "#!/bin/sh\n" +
		"printf '%s|%s|%s\\n' \"$PWD\" \"$PREMISE_TEMPLATE_TEST\" \"$*\" >> \"$MISE_LOG\"\n" +
		"case \"$PWD\" in *\"$MISE_FAIL_DIRECTORY\"*) exit 7;; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISE_LOG", log)
	t.Setenv("MISE_FAIL_DIRECTORY", failDirectoryFragment)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func assertGenerationFailureCleanup(t *testing.T, workspace, relativeDestination, manifestPath string, before []byte, temp string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(workspace, relativeDestination)); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failure: %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("manifest changed after failed generation:\n%s", after)
	}
	for _, root := range []string{filepath.Join(workspace, filepath.Dir(relativeDestination)), temp} {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "premise-stage-") || strings.Contains(entry.Name(), "premise-selected-") || strings.Contains(entry.Name(), "premise-tool-preflight-") || strings.Contains(entry.Name(), "premise-candidate-validation-") {
				t.Fatalf("temporary directory leaked at %s", filepath.Join(root, entry.Name()))
			}
		}
	}
}
