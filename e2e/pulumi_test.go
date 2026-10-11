package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
)

// Public API -----------------------------------------------------------------

func TestPulumiWorkflow(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi")
	exercisePulumiWorkflow(t, filepath.Join(root, "infra-registry"), filepath.Join(root, "app-registry"))
}

// TestPulumiLocalState initializes and destroys empty fixture stacks with the
// real Pulumi CLI. It verifies inherited config and stable destroy confirmation.
func TestPulumiLocalState(t *testing.T) {
	if os.Getenv("PREMISE_PULUMI_LOCAL_STATE") != "1" {
		t.Skip("set PREMISE_PULUMI_LOCAL_STATE=1 to exercise the real local backend")
	}
	registry := filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/infra-registry")
	tools := t.TempDir()
	binary := filepath.Join(tools, "pm")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	t.Setenv("PULUMI_HOME", filepath.Join(tools, "pulumi-home"))
	t.Setenv("PULUMI_CONFIG_PASSPHRASE", "disposable-poc-test")
	workspace := filepath.Join(t.TempDir(), "client")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runCLI(t, binary, workspace, "init")
	for _, name := range []string{"shared", "environment"} {
		runCLI(t, binary, workspace, "g", registry+":gcs-bucket", "--answer", "name="+name)
	}
	// A minimal app fixture isolates real stack configuration from Bun tooling.
	manifestPath := filepath.Join(workspace, core.ManifestFilename)
	manifest, err := core.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, core.Project{Name: "site", Template: "fixture:app", Path: "apps/site"})
	if err := os.MkdirAll(filepath.Join(workspace, "apps/site"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	runCLI(t, binary, workspace, "infra", "bind", "site", "environment")
	// Running the known generated build also lets Mise trust its active config
	// before a Pulumi shim discovers the workspace config through PATH.
	runCLI(t, binary, workspace, "run", "environment:build")
	for _, environment := range []string{"dev", "stage", "prod"} {
		runCLI(t, binary, workspace, "infra", "init", "--all", "--env", environment, "--gcp-project", "test-project", "--location", "US")
	}
	for name, stacks := range map[string][]string{"shared": {"shared"}, "environment": {"dev", "stage", "prod"}} {
		for _, stack := range stacks {
			data, err := os.ReadFile(filepath.Join(workspace, infraProgramPath(name), "Pulumi."+stack+".yaml"))
			if err != nil || !strings.Contains(string(data), "gcp:project: test-project") || !strings.Contains(string(data), name+":location: US") {
				t.Fatalf("stack settings %s/%s: %v\n%s", name, stack, err, data)
			}
			if name == "environment" {
				command := exec.Command("pulumi", "config", "get", "artifacts", "--stack", stack)
				command.Dir = filepath.Join(workspace, infraProgramPath(name))
				output, err := command.CombinedOutput()
				if err != nil || !strings.Contains(string(output), "../../apps/site/dist") {
					t.Fatalf("inherited binding %s: %v\n%s", stack, err, output)
				}
			}
			projectData, _ := os.ReadFile(filepath.Join(workspace, infraProgramPath(name), "Pulumi.yaml"))
			if name == "environment" && !strings.Contains(string(projectData), "site: ../../apps/site/dist") {
				t.Fatalf("real Pulumi initialization lost the template binding: %s", data)
			}
		}
	}
	// A real non-terminal destroy must refuse stable stacks, even when Pulumi's
	// ambient setting normally skips confirmation.
	t.Setenv("PULUMI_SKIP_CONFIRMATIONS", "true")
	destroy := exec.Command(binary, "infra", "destroy", "environment", "--env", "dev")
	destroy.Dir = workspace
	if output, err := destroy.CombinedOutput(); err == nil || !strings.Contains(string(output), "non-interactive mode") {
		t.Fatalf("stable destroy did not require confirmation: %v\n%s", err, output)
	}
	runCLI(t, binary, workspace, "infra", "init", "environment", "--env", "preview", "--branch", "feature/lifecycle", "--gcp-project", "test-project", "--location", "US")
	runCLI(t, binary, workspace, "infra", "destroy", "environment", "--env", "preview", "--branch", "feature/lifecycle")
	previewStack, err := core.InfraStack(core.InfraOptions{Environment: "preview", Branch: "feature/lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "infra/environment", "Pulumi."+previewStack+".yaml")); !os.IsNotExist(err) {
		t.Fatalf("preview config was not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".pulumi-state", ".pulumi")); err != nil {
		t.Fatalf("local state missing: %v", err)
	}
}

// Private helpers ------------------------------------------------------------

// exercisePulumiWorkflow checks orchestration, not GCP resource creation.
func exercisePulumiWorkflow(t *testing.T, pulumiRegistry, appRegistry string) {
	t.Helper()
	root := repositoryRoot(t)
	tools := t.TempDir()
	binary := filepath.Join(tools, "pm")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	logPath := filepath.Join(tools, "commands.log")

	for name, script := range map[string]string{
		"mise": `#!/bin/sh
printf 'mise|%s|%s\n' "$PWD" "$*" >> "${0%/*}/commands.log"
if [ "$1" = run ] && [ "$2" = build ] && [ -f package.json ]; then
 mkdir -p dist
 printf '<html>test build</html>' > dist/index.html
fi
`,
		"pulumi": `#!/bin/sh
printf 'pulumi|%s|%s\n' "$PWD" "$*" >> "${0%/*}/commands.log"
if [ "$1" = destroy ]; then printf 'confirmations|%s\n' "$PULUMI_SKIP_CONFIRMATIONS" >> "${0%/*}/commands.log"; fi
if [ "$1" = up ] && [ -f "${0%/*}/fail-infra" ]; then exit 1; fi
if [ "$1 $2 $3" = 'stack output bucketName' ]; then printf 'test-artifacts-dev\n'; fi
`,
	} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PULUMI_SKIP_CONFIRMATIONS", "true")
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	workspace := filepath.Join(t.TempDir(), "client")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runCLI(t, binary, workspace, "init")
	for _, name := range []string{"shared", "environment"} {
		runCLI(t, binary, workspace, "g", pulumiRegistry+":gcs-bucket", "--answer", "name="+name)
	}
	runCLI(t, binary, workspace, "g", appRegistry+":static-app", "--answer", "name=site")
	runCLI(t, binary, workspace, "infra", "bind", "site", "environment", "--artifact", "dist")
	data, err := os.ReadFile(filepath.Join(workspace, "infra/environment/Pulumi.yaml"))
	if err != nil || !strings.Contains(string(data), "site: ../../apps/site/dist") {
		t.Fatalf("project binding: %v\n%s", err, data)
	}
	runCLI(t, binary, workspace, "run", "site:build")
	manifest, err := core.LoadManifest(filepath.Join(workspace, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Workspace.Projects) != 3 {
		t.Fatalf("projects: %v", manifest.Workspace.Projects)
	}
	componentPath := filepath.Join(workspace, "infra/components/client-bucket")
	if err := os.MkdirAll(componentPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(componentPath, "mise.toml"), []byte("[tasks.test]\nrun = 'true'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, core.Project{Name: "client-bucket", Path: "infra/components/client-bucket"})
	if err := core.SaveManifest(filepath.Join(workspace, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shared", "environment"} {
		data, err := os.ReadFile(filepath.Join(workspace, infraProgramPath(name), "Pulumi.yaml"))
		if err != nil || !strings.Contains(string(data), "name: "+name) {
			t.Fatalf("rendered project %s: %v\n%s", name, err, data)
		}
	}
	for _, environment := range []string{"dev", "stage", "prod"} {
		runCLI(t, binary, workspace, "infra", "init", "--all", "--env", environment, "--gcp-project", "test-project", "--location", "US")
		runCLI(t, binary, workspace, "infra", "plan", "--env", environment)
		runCLI(t, binary, workspace, "infra", "apply", "--env", environment, "--yes")
	}
	runCLI(t, binary, workspace, "deploy", "on-deploy", "--env", "dev", "--yes")
	data, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	trace := string(data)
	for _, command := range []string{"stack select shared --create", "stack select dev --create", "stack select prod --create", "up --stack shared --yes", "up --stack dev --yes", "up --stack prod --yes"} {
		if !strings.Contains(trace, command) {
			t.Fatalf("missing %q\n%s", command, trace)
		}
	}
	sharedUpdate := strings.Index(trace, "up --stack shared")
	devUpdate := strings.Index(trace, "up --stack dev")
	if sharedUpdate < 0 || sharedUpdate > devUpdate {
		t.Fatalf("shared must apply first\n%s", trace)
	}
	for _, args := range [][]string{
		{"infra", "plan", "--env", "preview", "--branch", "feature/example"},
		{"infra", "destroy", "--env", "preview", "--branch", "feature/example"},
		{"infra", "destroy", "environment", "--env", "prod"},
		{"infra", "test"},
		{"deploy", "run", "site", "--env", "stage", "--task", "deploy"},
	} {
		runCLI(t, binary, workspace, args...)
	}
	extra, _ := os.ReadFile(logPath)
	if !strings.Contains(string(extra), "confirmations|false") || !strings.Contains(string(extra), "confirmations|true") {
		t.Fatalf("destroy confirmation policy: %s", extra)
	}
	if !strings.Contains(string(extra), "-- --environment stage --stack stage") {
		t.Fatalf("deployment arguments: %s", extra)
	}
	if !strings.Contains(string(extra), "infra/components/client-bucket|run test") {
		t.Fatalf("component test was not run: %s", extra)
	}

	for _, expected := range []string{"destroy --stack preview-feature-example-", "--non-interactive --remove", "destroy --stack prod"} {
		if !strings.Contains(string(extra), expected) {
			t.Fatalf("missing %q: %s", expected, extra)
		}
	}
	if strings.Contains(string(extra), "destroy --stack shared") || strings.Contains(string(extra), "destroy --stack prod --yes") {
		t.Fatalf("unsafe destroy: %s", extra)
	}
	// A failed shared update must stop before applying the environment stack.
	if err := os.WriteFile(filepath.Join(tools, "fail-infra"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "infra", "apply", "--all", "--env", "dev", "--yes")
	command.Dir = workspace
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("expected failed infrastructure update\n%s", output)
	}
	data, _ = os.ReadFile(logPath)
	if strings.Contains(string(data), "up --stack dev") || strings.Contains(string(data), "gcloud|") {
		t.Fatalf("continued after failed shared update\n%s", data)
	}
	for _, args := range [][]string{
		{"infra", "apply", "environment"},
		{"infra", "destroy", "--env", "prod", "--yes"},
		{"infra", "destroy", "--shared", "--yes"},
		{"infra", "plan", "environment", "--env", "dev", "--backend", "gs://state"},
		{"infra", "bind", "site", "shared"},
	} {
		command := exec.Command(binary, args...)
		command.Dir = workspace
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("expected rejection for %v\n%s", args, output)
		}
	}
}

func infraProgramPath(name string) string {
	if name == "environment" {
		return "infra/environment"
	}
	return filepath.Join("infra/projects", name)
}
