package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
)

func TestFreshBunScaffoldIsCIAndReleaseReady(t *testing.T) {
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "pm")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}

	log := installMiseTaskShim(t)
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runCLI(t, binary, workspace, "init")
	registry := filepath.Join(base, "registry")
	bunReleaseRegistryFixture(t, registry)
	if err := core.Generate(context.Background(), workspace, registry+":difflab-cli", fixedOptions(map[string]string{"name": "difflab-cli"}), os.Stdout); err != nil {
		t.Fatalf("generate: %v", err)
	}
	manifest, err := core.LoadManifest(filepath.Join(workspace, core.ManifestFilename))
	if err != nil {
		t.Fatalf("load generated manifest: %v", err)
	}
	if len(manifest.Workspace.PackageManagers) != 1 || manifest.Workspace.PackageManagers[0] != "bun" {
		t.Fatalf("workspace package_managers = %#v, want [bun]", manifest.Workspace.PackageManagers)
	}

	project := filepath.Join(workspace, "apps", "difflab-cli")
	assertFileContains(t, filepath.Join(project, "package.json"), `"name":"@difflab/difflab-cli"`)
	assertFileContains(t, filepath.Join(project, "package.json"), `"format:check"`)
	for _, name := range []string{"on-commit.yml", "on-merge.yml", "on-deploy.yml"} {
		path := filepath.Join(workspace, ".github", "workflows", name)
		assertFileContains(t, path, "cloudvoyant/premise@v0")
		if name == "on-commit.yml" {
			workflow, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(workflow), "github.event.before") {
				t.Fatal("on-commit must validate the first push to a new branch")
			}
		}
		if name == "on-merge.yml" {
			assertFileContains(t, path, "NODE_AUTH_TOKEN")
			assertFileContains(t, path, "secrets.NPM_TOKEN")
		}
	}

	install := exec.Command(binary, "install")
	install.Dir = workspace
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("pm install: %v\n%s", err, out)
	}
	ciStart := len(log.Invocations(t))
	ci := exec.Command(binary, "ci", "flow", "on-commit", "--release", "none")
	ci.Dir = workspace
	if out, err := ci.CombinedOutput(); err != nil {
		t.Fatalf("ci flow: %v\n%s", err, out)
	}
	invocations := log.Invocations(t)
	ciInvocations := invocations[ciStart:]
	for _, invocation := range ciInvocations {
		if invocation.Arguments == "run format" {
			t.Fatal("CI ran formatting task; want format:check only")
		}
	}
	workspaceInvocations := make([]string, 0)
	for _, invocation := range invocations {
		if invocation.Directory == workspace {
			workspaceInvocations = append(workspaceInvocations, invocation.Arguments)
		}
	}
	installIndex, formatCheckIndex := -1, -1
	for index, arguments := range workspaceInvocations {
		if arguments == "install --monorepo" {
			installIndex = index
		}
	}
	for index, invocation := range invocations {
		if invocation.Arguments == "run format:check" {
			formatCheckIndex = index
			break
		}
	}
	if installIndex == -1 || formatCheckIndex == -1 {
		t.Fatalf("install/CI invocation order = %#v, want install --monorepo before run format:check", workspaceInvocations)
	}

	git(t, workspace, "init", "-q")
	git(t, workspace, "config", "user.email", "test@example.invalid")
	git(t, workspace, "config", "user.name", "Test")
	git(t, workspace, "add", ".")
	git(t, workspace, "commit", "-qm", "feat: initial scaffold")
	git(t, workspace, "commit", "--allow-empty", "-qm", "feat: publish candidate [publish-rc]")
	t.Setenv("MISE_FAIL_DIRECTORY", workspace)
	t.Setenv("MISE_FAIL_ARGUMENT", "run //...:publish:rc")
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_REF", "refs/heads/feature/test")
	failedPublish := exec.Command(binary, "ci", "flow", "on-commit", "--release", "auto")
	failedPublish.Dir = workspace
	if out, err := failedPublish.CombinedOutput(); err == nil {
		t.Fatalf("failing publish task succeeded: %s", out)
	}
	if refs := gitOutput(t, workspace, "show-ref", "--tags"); refs != "" {
		t.Fatalf("failing publish task created release/tag seam output: %s", refs)
	}

	release := exec.Command(binary, "release", "--dry-run")
	release.Dir = workspace
	out, err := release.CombinedOutput()
	if err != nil {
		t.Fatalf("release plan: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "version=0.1.0") {
		t.Fatalf("release plan lacks v0.1.0: %s", out)
	}
	if refs := gitOutput(t, workspace, "show-ref", "--tags"); refs != "" {
		t.Fatalf("release created bootstrap tag: %s", refs)
	}

	preflight := exec.Command(binary, "release", "--dry-run")
	preflight.Dir = workspace
	if out, err := preflight.CombinedOutput(); err != nil {
		t.Fatalf("structural preflight: %v\n%s", err, out)
	}
	assertFileContains(t, filepath.Join(project, "package.json"), "@difflab/difflab-cli")
}

func bunReleaseRegistryFixture(t *testing.T, root string) {
	t.Helper()
	manifest := core.NewManifest("bun-release")
	manifest.Workspace.PackageManagers = []string{"bun"}
	manifest.TemplateRegistry = &core.TemplateRegistry{WorkspaceFiles: []string{"package.json", "mise.toml"}, Templates: []core.Template{{Name: "difflab-cli", Kind: "app", Path: "templates/difflab-cli", Questions: []core.Question{{Prompt: "name", Type: "string", Populate: "name"}}, Substitutions: map[string]string{"difflab-cli": "name"}}}}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "package.json"), `{"private":true,"workspaces":["apps/*"]}
`, 0o644)
	writeFixtureFile(t, filepath.Join(root, "mise.toml"), "[tools]\nbun = '1.2'\n", 0o644)
	p := filepath.Join(root, "templates", "difflab-cli")
	writeFixtureFile(t, filepath.Join(p, "package.json"), `{"name":"@difflab/difflab-cli","version":"0.1.0","publishConfig":{"access":"public","registry":"https://registry.example.invalid"},"scripts":{"install":"echo install","format:check":"echo format-check","format":"echo format","publish":"false"}}
`, 0o644)
	writeFixtureFile(t, filepath.Join(p, "mise.toml"), "[tasks.install]\nrun = 'bun install'\n[tasks.format-check]\nrun = 'bun run format:check'\n[tasks.format]\nrun = 'bun run format'\n[tasks.publish:rc]\nrun = 'false'\n[tasks.build]\nrun = 'bun run build'\n", 0o644)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out))
}
