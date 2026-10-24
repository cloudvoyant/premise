package backends

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
	"github.com/pelletier/go-toml/v2"
)

// Tests ----------------------------------------------------------------------

func TestNativeWorkspaceGeneration(t *testing.T) {
	registerBuiltins(t)
	t.Run("when extending an existing Cargo workspace", func(t *testing.T) {
		t.Run("it preserves client configuration outside membership", func(t *testing.T) {
			root, stage := t.TempDir(), t.TempDir()
			original, err := os.ReadFile("../testdata/package-generation/Cargo.toml")
			if err != nil {
				t.Fatal(err)
			}
			writePackageFixtureFile(t, root, "Cargo.toml", string(original))
			writePackageFixtureFile(t, stage, "Cargo.toml", "[package]\nname = 'tool'\nversion = '0.1.0'\nedition = '2024'\n")

			plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"cargo"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app"})
			if err != nil {
				t.Fatal(err)
			}
			discardGenerationPlan(t, plan, root, "apps/tool")

			output := generationOutput(t, plan, "Cargo.toml")
			var result struct {
				Workspace struct {
					Members  []string `toml:"members"`
					Resolver string   `toml:"resolver"`
				} `toml:"workspace"`
			}
			if err := toml.Unmarshal(output, &result); err != nil {
				t.Fatal(err)
			}
			if strings.Join(result.Workspace.Members, ",") != "libs/existing,apps/tool" || result.Workspace.Resolver != "2" {
				t.Fatalf("workspace configuration = %#v", result)
			}
			for _, expected := range []string{"# Client root identity stays here.", "# Preserve this comment", "default-members = [\"libs/existing\"]", "lto = true # Keep profile comments.", "[workspace.dependencies]", "[workspace.metadata.client]"} {
				if !strings.Contains(string(output), expected) {
					t.Fatalf("client content lost: %s\n%s", expected, output)
				}
			}
			unchanged, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
			if err != nil || string(unchanged) != string(original) {
				t.Fatal("planning changed the client root")
			}
		})
	})
	t.Run("when Cargo membership uses another native layout", func(t *testing.T) {
		for _, original := range []string{"[workspace]\nresolver = '2'\n[workspace.package]\nlicense = 'MIT'\n", "workspace.members = ['libs/existing']\n", "[workspace.package]\nlicense = 'MIT'\n"} {
			t.Run("it extends "+strings.Split(original, "\n")[0], func(t *testing.T) {
				root, stage := t.TempDir(), t.TempDir()
				writePackageFixtureFile(t, root, "Cargo.toml", original)
				writePackageFixtureFile(t, stage, "Cargo.toml", "[package]\nname='tool'\nversion='0.1.0'\nedition='2024'\n")

				plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"cargo"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app"})
				if err != nil {
					t.Fatal(err)
				}
				discardGenerationPlan(t, plan, root, "apps/tool")

				var result struct {
					Workspace struct {
						Members []string `toml:"members"`
					} `toml:"workspace"`
				}
				if err := toml.Unmarshal(generationOutput(t, plan, "Cargo.toml"), &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Workspace.Members) == 0 || result.Workspace.Members[len(result.Workspace.Members)-1] != "apps/tool" {
					t.Fatalf("member not added: %#v", result)
				}
			})
		}
	})
	t.Run("when a native root excludes the selected project", func(t *testing.T) {
		for _, manager := range []string{"cargo", "bun"} {
			t.Run("it rejects "+manager+" membership before publication", func(t *testing.T) {
				root, stage := t.TempDir(), t.TempDir()
				if manager == "cargo" {
					writePackageFixtureFile(t, root, "Cargo.toml", "[workspace]\nmembers=[]\nexclude=['apps/*']\n")
					writePackageFixtureFile(t, stage, "Cargo.toml", "[package]\nname='tool'\nversion='0.1.0'\n")
				} else {
					writePackageFixtureFile(t, root, "package.json", `{"workspaces":["apps/*","!apps/tool"]}`)
					writePackageFixtureFile(t, stage, "package.json", `{"name":"tool"}`)
				}

				plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{manager}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app"})

				if plan != nil {
					discardGenerationPlan(t, plan, root, "apps/tool")
				}
				if err == nil || !strings.Contains(err.Error(), "excludes") {
					t.Fatalf("expected native exclusion error, got %v", err)
				}
				if _, err := os.Stat(filepath.Join(root, "apps/tool")); !os.IsNotExist(err) {
					t.Fatalf("destination exists: %v", err)
				}
			})
		}
	})
	t.Run("when existing Go configuration defines multiple modules", func(t *testing.T) {
		for _, path := range []string{"go.work", "libs/existing/go.mod"} {
			t.Run("it rejects "+path+" without converting client files", func(t *testing.T) {
				root, stage := t.TempDir(), t.TempDir()
				original := "module example.com/standalone\ngo 1.25.0\n"
				if path == "go.work" {
					original = "go 1.25.0\nuse ./libs/existing\n"
				}
				writePackageFixtureFile(t, root, path, original)
				writePackageFixtureFile(t, stage, "go.mod", "module example.com/workspace/libs/new\ngo 1.25.0\n")

				plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"go"}, ProjectPaths: []string{"libs/existing"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "libs/new", TemplateKind: "lib"})

				if plan != nil {
					discardGenerationPlan(t, plan, root, "libs/new")
				}
				if err == nil || !strings.Contains(err.Error(), "root module") {
					t.Fatalf("expected existing module layout error: %v", err)
				}
				data, readErr := os.ReadFile(filepath.Join(root, path))
				if readErr != nil || string(data) != original {
					t.Fatal("existing Go layout was changed")
				}
				if _, err := os.Stat(filepath.Join(root, "libs/new")); !os.IsNotExist(err) {
					t.Fatalf("new project exists after layout rejection: %v", err)
				}
			})
		}
	})

	t.Run("when Bun contributes shared tooling", func(t *testing.T) {
		t.Run("it merges disjoint keys and preserves client identity and unknown fields", func(t *testing.T) {
			root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
			original, err := os.ReadFile("../testdata/package-generation/package.json")
			if err != nil {
				t.Fatal(err)
			}
			writePackageFixtureFile(t, root, "package.json", string(original))
			writePackageFixtureFile(t, registry, "package.json", `{"name":"registry-only","private":true,"workspaces":["templates/*"],"scripts":{"shared":"echo shared"},"devDependencies":{"new-tool":"^3.0.0"}}`)
			writePackageFixtureFile(t, stage, "package.json", `{"name":"tool","dependencies":{"project-only":"1.0.0"}}`)

			plan, err := core.BuildGeneratePlan(core.GenerateParameters{WorkspaceName: "client", PackageManagers: []string{"bun"}, RegistryRoot: registry, RegistryWorkspaceFiles: []string{"package.json"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app", ResolveConflict: core.MergeDecisions{}.Resolve})
			if err != nil {
				t.Fatal(err)
			}
			discardGenerationPlan(t, plan, root, "apps/tool")

			var result map[string]any
			if err := json.Unmarshal(generationOutput(t, plan, "package.json"), &result); err != nil {
				t.Fatal(err)
			}
			if result["name"] != "client-owned-root" || result["private"] != false || result["version"] != "7.0.0" || result["clientMetadata"] == nil {
				t.Fatalf("client identity lost: %#v", result)
			}
			output := string(generationOutput(t, plan, "package.json"))
			for _, expected := range []string{"existing-tool", "new-tool", "nohoist", "apps/tool", "echo client", "echo shared"} {
				if !strings.Contains(output, expected) {
					t.Fatalf("missing %q in %s", expected, output)
				}
			}
			if strings.Contains(output, "templates/") || strings.Contains(output, "project-only") {
				t.Fatalf("registry membership or project dependencies leaked into root: %s", output)
			}
			if len(plan.Entries) != 1 {
				t.Fatalf("native root was merged more than once: %#v", plan.Entries)
			}
		})
		t.Run("it reports a focused dependency conflict instead of replacing the root", func(t *testing.T) {
			root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
			writePackageFixtureFile(t, root, "package.json", `{"name":"client","devDependencies":{"tool":"^1"}}`)
			writePackageFixtureFile(t, registry, "package.json", `{"devDependencies":{"tool":"^2"}}`)
			writePackageFixtureFile(t, stage, "package.json", `{"name":"app"}`)

			_, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"bun"}, RegistryRoot: registry, RegistryWorkspaceFiles: []string{"package.json"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app", ResolveConflict: core.MergeDecisions{}.Resolve})

			if err == nil || !strings.Contains(err.Error(), "package.json.devDependencies.tool") {
				t.Fatalf("expected focused conflict: %v", err)
			}
			original, err := os.ReadFile(filepath.Join(root, "package.json"))
			if err != nil || !strings.Contains(string(original), `"tool":"^1"`) {
				t.Fatal("aborted merge changed the client")
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func generationOutput(t *testing.T, plan *core.GeneratePlan, path string) []byte {
	t.Helper()
	for _, entry := range plan.Entries {
		if entry.Path == path {
			return entry.Output.Data
		}
	}
	t.Fatalf("no planned output for %s", path)
	return nil
}

// Cancelled application closes the public plan without executing native tools or publishing.
func discardGenerationPlan(t *testing.T, plan *core.GeneratePlan, root, project string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := core.ApplyGeneratePlan(ctx, plan, io.Discard); err == nil {
			t.Error("cancelled generation unexpectedly succeeded")
		}
		if _, err := os.Stat(filepath.Join(root, project)); !os.IsNotExist(err) {
			t.Errorf("cancelled generation published a project: %v", err)
		}
		stages, err := filepath.Glob(filepath.Join(root, filepath.Dir(project), ".premise-stage-*"))
		if err != nil || len(stages) != 0 {
			t.Errorf("generation stages remain: %v %v", stages, err)
		}
	})
}
