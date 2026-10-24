package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/backends"
)

// Public API -----------------------------------------------------------------

func TestScaffold(t *testing.T) {
	if err := backends.RegisterPackageManagers(); err != nil {
		t.Fatal(err)
	}
	binary := premiseBinary(t)
	t.Setenv("PULUMI_HOME", t.TempDir())

	t.Run("when creating a lib", func(t *testing.T) {
		// Arrange and act.
		workspace := scaffoldProject(t, binary, "lib", false)
		project := filepath.Join(workspace, "libs", "example")

		// Assert.
		t.Run("it scaffolds commit and merge actions", func(t *testing.T) { assertScaffoldWorkflows(t, workspace, false) })
		t.Run("it replaces template strings with the project name", func(t *testing.T) {
			assertFileContains(t, filepath.Join(project, "go.mod"), "module example.com/example")
			assertFileContains(t, filepath.Join(project, "greeting.go"), `return "example"`)
			assertFileContains(t, filepath.Join(project, "README.md"), "A runnable example project.")
		})
		t.Run("it creates a buildable and testable library", func(t *testing.T) {
			runCommand(t, workspace, binary, "run", "example:build")
			runCommand(t, workspace, binary, "run", "example:test")
		})
		t.Run("it provides library contracts without app-only tasks", func(t *testing.T) { assertProjectContracts(t, project, false) })
		t.Run("it does not schedule infrastructure deployments", func(t *testing.T) {
			plan := runCommand(t, workspace, binary, "ci", "plan", "--ci-output")
			if !strings.Contains(plan, "has_infra=false\n") {
				t.Fatalf("library scheduled infrastructure: %s", plan)
			}
		})
	})

	t.Run("when creating an app without infra", func(t *testing.T) {
		// Arrange and act.
		workspace := scaffoldProject(t, binary, "app", false)
		project := filepath.Join(workspace, "apps", "example")

		// Assert.
		t.Run("it scaffolds commit and merge actions", func(t *testing.T) { assertScaffoldWorkflows(t, workspace, false) })
		t.Run("it replaces template strings with the project name", func(t *testing.T) {
			assertFileContains(t, filepath.Join(project, "go.mod"), "module example.com/example")
			assertFileContains(t, filepath.Join(project, "main.go"), `fmt.Println("example")`)
		})
		t.Run("it creates a runnable app", func(t *testing.T) {
			output := runCommand(t, workspace, binary, "run", "example:run")
			if strings.TrimSpace(output) != "example" {
				t.Fatalf("app output = %q", output)
			}
		})
		t.Run("it provides app contracts in addition to common contracts", func(t *testing.T) { assertProjectContracts(t, project, true) })
		t.Run("it omits the infra directory and manual deployment action", func(t *testing.T) {
			assertAbsent(t, filepath.Join(workspace, "infra"))
			assertAbsent(t, filepath.Join(workspace, ".github/workflows/on-deploy.yml"))
		})
	})

	t.Run("when creating an app with infra", func(t *testing.T) {
		requireLiveInfrastructure(t)
		// Arrange and act: use the actual Pulumi starter, not a simulated CLI.
		workspace := scaffoldProject(t, binary, "app", true)
		client := configureLiveClient(t, binary, workspace)

		// Assert.
		t.Run("it scaffolds the program and manual deployment action", func(t *testing.T) {
			assertScaffoldWorkflows(t, workspace, true)
			assertFileContains(t, filepath.Join(workspace, "infra/Pulumi.yaml"), "runtime: go")
			assertFileContains(t, filepath.Join(workspace, "mise.toml"), "pulumi")
		})
		t.Run("it leaves working infra setup plan and output commands", func(t *testing.T) {
			client.infra(t, "setup", "dev")
			client.infra(t, "plan", "dev")
			client.infra(t, "apply", "dev", "--yes")
			if output := strings.TrimSpace(client.infra(t, "output", "dev")); output != "{}" {
				t.Fatalf("empty starter outputs = %q", output)
			}
		})
	})

	t.Run("when adding infra after scaffolding", func(t *testing.T) {
		requireLiveInfrastructure(t)
		// Arrange.
		workspace := scaffoldProject(t, binary, "app", false)
		workflow := filepath.Join(workspace, ".github/workflows/on-commit.yml")
		before := readFile(t, workflow)

		// Act.
		runCommand(t, workspace, binary, "infra", "setup", "--pulumi-lang", "go")

		// Assert.
		t.Run("it adds the infra directory and missing deployment action", func(t *testing.T) {
			assertScaffoldWorkflows(t, workspace, true)
			assertFileContains(t, filepath.Join(workspace, "infra/Pulumi.yaml"), "runtime: go")
		})
		t.Run("it preserves the app and existing workflow", func(t *testing.T) {
			assertFileContent(t, workflow, before)
			assertFileContains(t, filepath.Join(workspace, "apps/example/main.go"), `fmt.Println("example")`)
		})
		t.Run("it preserves tool pins when setup runs again", func(t *testing.T) {
			before := readFile(t, filepath.Join(workspace, "mise.toml"))
			runCommand(t, workspace, binary, "infra", "setup")
			assertFileContent(t, filepath.Join(workspace, "mise.toml"), before)
		})
		t.Run("it leaves a working environment plan", func(t *testing.T) {
			client := configureLiveClient(t, binary, workspace)
			client.infra(t, "setup", "dev")
			client.infra(t, "plan", "dev")
		})
	})

	t.Run("when generating native workspaces", testNativeWorkspaceGeneration)
	t.Run("when combining native workspaces", testMixedNativeWorkspaceGeneration)
	t.Run("when extending an existing standalone Cargo workspace", testExistingStandaloneCargoGeneration)
	t.Run("when native generation inputs conflict", testNativeGenerationConflicts)

	t.Run("when validating workspace and template inputs", func(t *testing.T) {
		t.Run("it requires an initialized workspace for generation", testCLIGenerateRequiresInitializedWorkspace)
		t.Run("it requires the workspace root for installation", testCLIInstallRequiresWorkspaceRoot)
		t.Run("it supports workspace and template registry commands", testCLIWorkspaceAndTemplateLifecycle)
		t.Run("it preserves binary assets modes and project provenance", testWorkspaceTemplateAndGenerationWorkflow)
		t.Run("it combines Cargo and Bun registry contributions", testGenerateFromCargoAndBunRegistries)
		t.Run("it leaves no live state when validation fails", testGenerateValidationFailuresLeaveNoLiveState)
		t.Run("it explains a missing template manifest", testGenerateExplainsMissingTemplateManifest)
		t.Run("it preserves existing Mise configuration", testInitializeWorkspacePreservesExistingMiseConfig)
		t.Run("it rejects Mise configuration without a monorepo marker", testInitializeWorkspaceRejectsExistingMiseWithoutMonorepoMarker)
		t.Run("it rejects unknown manifest fields", testManifestRejectsUnknownFields)
		t.Run("it rejects duplicate projects", testConfigRejectsDuplicateProjects)
		t.Run("it reports all template contract failures", testTemplateContractsCollectAllFailures)
	})
}

// Private helpers ------------------------------------------------------------

func scaffoldProject(t *testing.T, binary, kind string, infrastructure bool) string {
	t.Helper()
	base := t.TempDir()
	registry := filepath.Join(base, "registry")
	workspace := filepath.Join(base, "workspace")
	for _, directory := range []string{registry, workspace} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	runCommand(t, registry, binary, "init", "--kind", "template-registry")
	runCommand(t, registry, binary, "template", "init", kind)
	copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/scaffold", kind), filepath.Join(registry, "templates", kind))
	args := []string{"init"}
	if infrastructure {
		args = append(args, "--infra", "--pulumi-lang", "go")
	}
	runCommand(t, workspace, binary, args...)
	runCommand(t, workspace, binary, "generate", registry+":"+kind, "--answer", "name=example")
	return workspace
}

func assertScaffoldWorkflows(t *testing.T, workspace string, infrastructure bool) {
	t.Helper()
	for _, name := range []string{"on-commit", "on-merge"} {
		assertFileContains(t, filepath.Join(workspace, ".github/workflows", name+".yml"), "cloudvoyant/premise@v")
	}
	deploy := filepath.Join(workspace, ".github/workflows/on-deploy.yml")
	if infrastructure {
		assertFileContains(t, deploy, "workflow_dispatch:")
	} else {
		assertAbsent(t, deploy)
	}
}

func assertProjectContracts(t *testing.T, project string, application bool) {
	t.Helper()
	data := readFile(t, filepath.Join(project, "mise.toml"))
	configuration, err := core.ExtractMiseConfig("example", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"install", "build", "clean", "test", "lint", "lint:fix", "format", "format:check", "env-pull", "publish:rc", "publish"} {
		if _, ok := configuration.Tasks[task]; !ok {
			t.Errorf("missing common contract %s", task)
		}
	}
	for _, task := range []string{"run", "dev", "deploy", "e2e"} {
		_, present := configuration.Tasks[task]
		if present != application {
			t.Errorf("app contract %s present = %t, want %t", task, present, application)
		}
	}
}
