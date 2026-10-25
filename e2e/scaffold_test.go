package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/backends"
)

// Tests ----------------------------------------------------------------------

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

func testNativeWorkspaceGeneration(t *testing.T) {
	binary := premiseBinary(t)
	for _, manager := range []string{"go", "cargo", "bun"} {
		t.Run("when generating a "+manager+" workspace", func(t *testing.T) {
			base := t.TempDir()
			workspace := filepath.Join(base, "client")
			if err := os.MkdirAll(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			registry := nativeRegistry(t, manager, filepath.Join(base, "registry"))
			runCommand(t, workspace, binary, "init")

			runCommand(t, workspace, binary, "generate", registry+":lib-template", "--answer", "name=helper,library=helper")
			initialRules := readFile(t, filepath.Join(workspace, ".editorconfig"))
			clientRules := initialRules + "\n# Client quality rules\n"
			writeFixtureFile(t, filepath.Join(workspace, ".editorconfig"), clientRules, 0644)
			// Arrange stale root and template locks; native installation must replace them.
			lock := ""
			switch manager {
			case "cargo":
				lock = "Cargo.lock"
			case "bun":
				lock = "bun.lock"
			}
			if lock != "" {
				writeFixtureFile(t, filepath.Join(workspace, lock), "stale lock fixture", 0640)
				writeFixtureFile(t, filepath.Join(registry, "templates/app-template", lock), "stale template lock fixture", 0644)
			}

			// Act
			runCommand(t, workspace, binary, "generate", registry+":app-template", "--answer", "name=tool,library=helper")
			var generatedLockErr error
			if manager == "cargo" || manager == "bun" {
				lock := "Cargo.lock"
				if manager == "bun" {
					lock = "bun.lock"
				}
				_, generatedLockErr = os.Stat(filepath.Join(workspace, lock))
			}
			runCommand(t, workspace, binary, "install")

			t.Run("it initializes quality rules once and preserves client changes", func(t *testing.T) {
				baseline := readFile(t, filepath.Join(registry, ".editorconfig"))
				if initialRules != baseline {
					t.Fatalf("initial rules = %q, want %q", initialRules, baseline)
				}
				assertFileContent(t, filepath.Join(workspace, ".editorconfig"), clientRules)
				assertAbsent(t, filepath.Join(workspace, "libs/helper/.editorconfig"))
				assertAbsent(t, filepath.Join(workspace, "apps/tool/.editorconfig"))
			})

			t.Run("it resolves the root and uses its existing sibling", func(t *testing.T) {
				if generatedLockErr != nil {
					t.Fatalf("generation discarded candidate lockfile: %v", generatedLockErr)
				}
				if lock != "" {
					if strings.Contains(readFile(t, filepath.Join(workspace, lock)), "stale") {
						t.Fatal("native installation did not replace the stale lockfile")
					}
					assertAbsent(t, filepath.Join(workspace, "apps/tool", lock))
					info, err := os.Stat(filepath.Join(workspace, lock))
					if err != nil || info.Mode().Perm() != 0640 {
						t.Fatalf("regenerated lock mode changed: %v %v", info, err)
					}
				}
				output := runCommand(t, workspace, binary, "run", "tool:run")
				if strings.TrimSpace(output) != "hello" {
					t.Fatalf("app output = %q", output)
				}
				switch manager {
				case "go":
					runCommand(t, workspace, "go", "test", "./...")
					assertAbsent(t, filepath.Join(workspace, "libs/helper/go.mod"))
					assertAbsent(t, filepath.Join(workspace, "apps/tool/go.mod"))
					assertAbsent(t, filepath.Join(workspace, "go.work"))
				case "cargo":
					output := runCommand(t, workspace, "cargo", "metadata", "--no-deps", "--format-version=1", "--offline")
					var metadata struct {
						WorkspaceMembers []string `json:"workspace_members"`
					}
					if err := json.Unmarshal([]byte(output), &metadata); err != nil {
						t.Fatal(err)
					}
					if len(metadata.WorkspaceMembers) != 2 {
						t.Fatalf("Cargo members = %v", metadata.WorkspaceMembers)
					}
					runCommand(t, workspace, "cargo", "fetch", "--offline")
					if _, err := os.Stat(filepath.Join(workspace, "Cargo.lock")); err != nil {
						t.Fatal(err)
					}
				case "bun":
					// Generation itself must retain the lock produced inside its candidate.
					if _, err := os.Stat(filepath.Join(workspace, "bun.lock")); err != nil {
						t.Fatal(err)
					}
					runCommand(t, filepath.Join(workspace, "apps/tool"), "bun", "install", "--ignore-scripts", "--frozen-lockfile")
					root := readFile(t, filepath.Join(workspace, "package.json"))
					if strings.Contains(root, "registry-development") || strings.Contains(root, "templates/") {
						t.Fatalf("registry root leaked: %s", root)
					}
				}
				manifest, err := core.LoadManifest(filepath.Join(workspace, core.ManifestFilename))
				if err != nil || len(manifest.Workspace.Projects) != 2 {
					t.Fatalf("project provenance missing: %#v %v", manifest, err)
				}
				assertNoGenerationTemporaryDirectories(t, filepath.Join(workspace, "apps"), filepath.Join(workspace, "libs"))
			})
		})
	}
}

func testMixedNativeWorkspaceGeneration(t *testing.T) {
	binary := premiseBinary(t)
	for _, order := range [][]string{{"bun", "cargo"}, {"cargo", "bun"}} {
		t.Run("when adding "+strings.Join(order, " then ")+" to Go", func(t *testing.T) {
			base := t.TempDir()
			workspace := filepath.Join(base, "client")
			if err := os.MkdirAll(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			runCommand(t, workspace, binary, "init")
			managers := append([]string{"go"}, order...)

			for _, manager := range managers {
				registry := nativeRegistry(t, manager, filepath.Join(base, "registry-"+manager))
				name := manager + "-helper"
				runCommand(t, workspace, binary, "generate", registry+":lib-template", "--answer", "name="+name+",library="+name)
				runCommand(t, workspace, binary, "generate", registry+":app-template", "--answer", "name="+manager+"-tool,library="+name)
			}

			runCommand(t, workspace, binary, "install")

			t.Run("it keeps all native workspaces usable", func(t *testing.T) {
				for _, manager := range managers {
					output := runCommand(t, workspace, binary, "run", manager+"-tool:run")
					if strings.TrimSpace(output) != "hello" {
						t.Fatalf("%s output: %q", manager, output)
					}
				}
				assertFileContains(t, filepath.Join(workspace, "Cargo.toml"), "libs/cargo-helper")
				assertFileContains(t, filepath.Join(workspace, "go.mod"), "module example.com/workspace")
				assertAbsent(t, filepath.Join(workspace, "libs/go-helper/go.mod"))
			})
			t.Run("when adding a language neutral template", func(t *testing.T) {
				registry := filepath.Join(base, "generic")
				manifest := core.NewManifest("generic")
				manifest.TemplateRegistry = &core.TemplateRegistry{WorkspaceFiles: []string{}, Templates: []core.Template{{Name: "notes", Kind: "lib", Path: "templates/notes", Questions: []core.Question{{Prompt: "Name:", Type: "string", Populate: "name"}}}}}
				if err := core.SaveManifest(filepath.Join(registry, core.ManifestFilename), manifest); err != nil {
					t.Fatal(err)
				}
				project := filepath.Join(registry, "templates/notes")
				copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/package-generation/generic"), project)
				originals := map[string]string{}
				for _, path := range []string{"go.mod", "Cargo.toml", "package.json"} {
					originals[path] = readFile(t, filepath.Join(workspace, path))
				}

				runCommand(t, workspace, binary, "generate", registry+":notes", "--answer", "name=notes")

				t.Run("it preserves native manifests and creates the project", func(t *testing.T) {
					for path, before := range originals {
						assertFileContent(t, filepath.Join(workspace, path), before)
					}
					assertFileContains(t, filepath.Join(workspace, "libs/notes/README.md"), "generic fixture")
				})
			})
		})
	}
}

func testExistingStandaloneCargoGeneration(t *testing.T) {
	binary := premiseBinary(t)
	base := t.TempDir()
	workspace := filepath.Join(base, "client")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	runCommand(t, workspace, binary, "init")
	old := filepath.Join(workspace, "libs/old-helper")
	copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/package-generation/cargo/lib"), old)
	path := filepath.Join(old, "Cargo.toml")
	writeFixtureFile(t, path, strings.ReplaceAll(readFile(t, path), "lib-template", "old-helper"), 0644)
	manifestPath := filepath.Join(workspace, core.ManifestFilename)
	manifest, err := core.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.PackageManagers = []string{"cargo"}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, core.Project{Name: "old-helper", Template: "legacy:lib", Path: "libs/old-helper"})
	if err := core.SaveManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	registry := nativeRegistry(t, "cargo", filepath.Join(base, "registry"))

	runCommand(t, workspace, binary, "generate", registry+":lib-template", "--answer", "name=new-helper,library=new-helper")
	runCommand(t, workspace, binary, "install")

	t.Run("it keeps existing declared crates in the new root workspace", func(t *testing.T) {
		metadata := runCommand(t, workspace, "cargo", "metadata", "--no-deps", "--format-version=1", "--offline")
		var result struct {
			WorkspaceMembers []string `json:"workspace_members"`
		}
		if err := json.Unmarshal([]byte(metadata), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.WorkspaceMembers) != 2 {
			t.Fatalf("existing crate lost during root creation: %s", metadata)
		}
		assertFileContains(t, filepath.Join(workspace, "Cargo.toml"), "libs/old-helper")
		assertFileContains(t, filepath.Join(workspace, "Cargo.toml"), "libs/new-helper")
		runCommand(t, old, "cargo", "test", "--offline")
	})
}

func testNativeGenerationConflicts(t *testing.T) {
	binary := premiseBinary(t)
	for _, manager := range []string{"go", "cargo", "bun"} {
		t.Run("when "+manager+" inputs conflict", func(t *testing.T) {
			base := t.TempDir()
			workspace := filepath.Join(base, "client")
			if err := os.MkdirAll(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			runCommand(t, workspace, binary, "init")
			registry := nativeRegistry(t, manager, filepath.Join(base, "registry"))
			path, original, expected := "go.mod", "module example.com/different\ngo 1.25.0\n", "workspace Go module"
			if manager == "cargo" {
				path, original, expected = "Cargo.toml", "[workspace]\nmembers=[]\nexclude=['libs/*']\nresolver='3'\n", "excludes generated crate"
			}
			if manager == "bun" {
				path, original, expected = "package.json", `{"name":"client","workspaces":["apps/*","!libs/*"]}`, "excludes generated project"
			}
			writeFixtureFile(t, filepath.Join(workspace, path), original, 0640)
			manifest := readFile(t, filepath.Join(workspace, core.ManifestFilename))

			output := commandFailure(t, workspace, binary, "generate", registry+":lib-template", "--answer", "name=helper,library=helper")

			t.Run("it reports the conflict and preserves the workspace", func(t *testing.T) {
				if !strings.Contains(output, expected) {
					t.Fatalf("expected native conflict %q: %s", expected, output)
				}
				assertFileContent(t, filepath.Join(workspace, path), original)
				assertFileContent(t, filepath.Join(workspace, core.ManifestFilename), manifest)
				info, err := os.Stat(filepath.Join(workspace, path))
				if err != nil || info.Mode().Perm() != 0640 {
					t.Fatalf("root mode changed: %v %v", info, err)
				}
				assertAbsent(t, filepath.Join(workspace, "libs/helper"))
				assertNoGenerationTemporaryDirectories(t, filepath.Join(workspace, "libs"))
			})
		})
	}
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
