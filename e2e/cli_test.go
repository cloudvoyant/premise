package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIGenerateRequiresInitializedWorkspace(t *testing.T) {
	repositoryRoot := repositoryRoot(t)
	binaryName := "premise"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	command := exec.Command(binary, "generate", ".:app")
	command.Dir = t.TempDir()
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("expected generate to reject an uninitialized directory")
	}
	const expected = "Not a premise project. Run pm init to initialize a premise project."
	if !strings.Contains(string(output), expected) {
		t.Fatalf("expected %q, got %v\n%s", expected, err, output)
	}
}

func TestCLIInstallRequiresWorkspaceRoot(t *testing.T) {
	repositoryRoot := repositoryRoot(t)
	binaryName := "premise"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runCLI(t, binary, workspace, "init")
	nested := filepath.Join(workspace, "scratch")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "install")
	command.Dir = nested
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "pm install must run from the premise workspace root") {
		t.Fatalf("expected workspace-root error, got %v\n%s", err, output)
	}
}

func TestCLIWorkspaceAndTemplateLifecycle(t *testing.T) {
	repositoryRoot := repositoryRoot(t)
	binaryName := "premise"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	help := exec.Command(binary, "--help")
	output, err := help.CombinedOutput()
	if err != nil {
		t.Fatalf("show CLI help: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Usage:\n  pm [command]") {
		t.Fatalf("expected help usage to name pm, got:\n%s", output)
	}

	workspace := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runCLI(t, binary, workspace, "init")
	if _, err := os.Stat(filepath.Join(workspace, "premise.yaml")); err != nil {
		t.Fatal(err)
	}
	expectedMise, err := os.ReadFile(filepath.Join(repositoryRoot, "templates", "mise.toml"))
	if err != nil {
		t.Fatalf("read embedded workspace mise template: %v", err)
	}
	generatedMise, err := os.ReadFile(filepath.Join(workspace, "mise.toml"))
	if err != nil {
		t.Fatalf("read generated workspace mise configuration: %v", err)
	}
	if string(generatedMise) != string(expectedMise) {
		t.Fatalf("installed binary did not embed workspace mise template:\n%s", generatedMise)
	}
	command := exec.Command(binary, "init")
	command.Dir = workspace
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "already exists") {
		t.Fatalf("expected overwrite refusal, got %v\n%s", err, output)
	}

	command = exec.Command(binary, "template", "init", "app")
	command.Dir = workspace
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "cannot add registry templates to a monorepo project") {
		t.Fatalf("expected hybrid-project refusal, got %v\n%s", err, output)
	}
	command = exec.Command(binary, "template", "ls")
	command.Dir = workspace
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "template ls requires a template registry") {
		t.Fatalf("expected template-registry requirement, got %v\n%s", err, output)
	}

	registryRoot := filepath.Join(t.TempDir(), "registry")
	if err := os.MkdirAll(registryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	runCLI(t, binary, registryRoot, "init", "--kind", "template-registry")
	runCLI(t, binary, registryRoot, "template", "init", "app")
	runCLI(t, binary, registryRoot, "template", "init", "lib")
	command = exec.Command(binary, "template", "ls")
	command.Dir = registryRoot
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("list registry templates: %v\n%s", err, output)
	}
	if got, want := string(output), "app\nlib\n"; got != want {
		t.Fatalf("template ls output = %q, want %q", got, want)
	}
	runCLI(t, binary, registryRoot, "template", "test")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(workingDirectory)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	return root
}

func runCLI(t *testing.T, binary, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pm %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
