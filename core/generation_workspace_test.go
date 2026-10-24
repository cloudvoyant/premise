package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures -------------------------------------------------------------------

type workspaceGenerationProvider struct {
	testPackageManager
	prepare func(PackageGenerationContext) ([]MergeEntry, error)
	paths   []string
}

// Tests ----------------------------------------------------------------------

func TestGenerateWorkspace(t *testing.T) {
	t.Run("when providers claim the same native root", func(t *testing.T) {
		t.Run("it rejects ownership before changing live files", func(t *testing.T) {
			useTestPackageManagers(t,
				workspaceGenerationProvider{testPackageManager: testPackageManager{id: "first"}},
				workspaceGenerationProvider{testPackageManager: testPackageManager{id: "second"}},
			)
			root, source := t.TempDir(), t.TempDir()
			writeTestFile(t, filepath.Join(source, "README.md"), "selected\n", 0644)

			plan, err := BuildGeneratePlan(GenerateParameters{PackageManagers: []string{"first", "second"}, ClientRepoRoot: root, TemplateRoot: source, ProjectPath: "libs/new", TemplateKind: "lib"})
			if plan != nil {
				t.Cleanup(func() { _ = plan.close() })
			}

			if err == nil || !strings.Contains(err.Error(), "owned") {
				t.Fatalf("expected ownership error, got %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "libs/new")); !os.IsNotExist(err) {
				t.Fatalf("destination exists: %v", err)
			}
		})
	})

	t.Run("when native preparation makes no root edits", func(t *testing.T) {
		for _, failure := range []bool{false, true} {
			outcome := "it validates existing source and publishes native outputs"
			if failure {
				outcome = "it discards native outputs after contract failure"
			}
			t.Run(outcome, func(t *testing.T) {
				useTestPackageManagers(t, workspaceGenerationProvider{testPackageManager: testPackageManager{id: "future"}})
				root, source := t.TempDir(), t.TempDir()
				writeTestFile(t, filepath.Join(root, "mise.toml"), "monorepo_root = true\n[tasks.install]\nrun = 'true'\n", 0644)
				writeTestFile(t, filepath.Join(root, "shared/helper.txt"), "existing source\n", 0644)
				writeTestFile(t, filepath.Join(root, "libs/bin/mise.toml"), "[tasks.install]\nrun = 'true'\n", 0644)
				writeTestFile(t, filepath.Join(root, ".cargo/config.toml"), "[net]\noffline = true\n", 0644)
				writeTestFile(t, filepath.Join(root, "future.lock"), "original\n", 0640)
				writeTestFile(t, filepath.Join(source, "mise.toml"), "[tasks.install]\nrun = 'true'\n", 0644)
				script, err := os.ReadFile("testdata/package-generation/mise.sh")
				if err != nil {
					t.Fatal(err)
				}
				bin := t.TempDir()
				writeTestFile(t, filepath.Join(bin, "mise"), string(script), 0755)
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				liveRoot, err := filepath.EvalSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
				t.Setenv("LIVE_WORKSPACE", liveRoot)
				t.Setenv("FAIL_BUILD", "")
				if failure {
					t.Setenv("FAIL_BUILD", "1")
				}
				plan, err := BuildGeneratePlan(GenerateParameters{PackageManagers: []string{"future"}, ClientRepoRoot: root, TemplateRoot: source, ProjectPath: "libs/new", TemplateKind: "lib"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = plan.close() })

				err = ApplyGeneratePlan(t.Context(), plan, io.Discard)

				if failure {
					if err == nil || !strings.Contains(err.Error(), "controlled candidate build failure") && !strings.Contains(err.Error(), "task build failed") {
						t.Fatalf("expected build failure, got %v", err)
					}
					if _, err := os.Stat(filepath.Join(root, "libs/new")); !os.IsNotExist(err) {
						t.Fatalf("destination exists: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(root, "future.lock"))
				if err != nil {
					t.Fatal(err)
				}
				want := "validated dependency output\n"
				if failure {
					want = "original\n"
				}
				if string(data) != want {
					t.Fatalf("native output = %q, want %q", data, want)
				}
				info, err := os.Stat(filepath.Join(root, "future.lock"))
				if err != nil || info.Mode().Perm() != 0640 {
					t.Fatalf("native output mode was not preserved: %v, %v", info, err)
				}
				if plan.stage != "" || plan.selectedStage != "" {
					t.Fatal("generation stages remain after execution")
				}
			})
		}
	})
	t.Run("when the incoming registry has no package manager", func(t *testing.T) {
		t.Run("it prepares already configured native roots using client identity", func(t *testing.T) {
			installMiseTestShim(t, false)
			provider := workspaceGenerationProvider{testPackageManager: testPackageManager{id: "future"}, prepare: func(request PackageGenerationContext) ([]MergeEntry, error) {
				return []MergeEntry{{Path: "future.lock", Output: PathValue{Present: true, Kind: "file", Data: []byte(request.WorkspaceName)}}}, nil
			}}
			useTestPackageManagers(t, provider)
			root := filepath.Join(t.TempDir(), "client")
			manifestPath, err := InitializeWorkspace(root, "monorepo_root = true\n")
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := LoadManifest(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			manifest.Workspace.PackageManagers = []string{"future"}
			if err := SaveManifest(manifestPath, manifest); err != nil {
				t.Fatal(err)
			}
			registry := writeRegistryFixture(t, templateFixture("generic", "lib"))

			err = Generate(t.Context(), root, registry+":generic", fixedGenerateOptions(fixedQuestionnaire{"name": "notes"}), io.Discard)

			if err != nil {
				t.Fatal(err)
			}
			output, err := os.ReadFile(filepath.Join(root, "future.lock"))
			if err != nil || string(output) != "client" {
				t.Fatalf("existing provider was omitted or used registry identity: %q %v", output, err)
			}
		})
	})

	t.Run("when a provider claims an unsafe root path", func(t *testing.T) {
		for _, path := range []string{"", "../outside", "nested/lock", ".git", "mise.toml"} {
			t.Run("it rejects "+path, func(t *testing.T) {
				useTestPackageManagers(t, workspaceGenerationProvider{testPackageManager: testPackageManager{id: "future"}, paths: []string{path}})
				root, source := t.TempDir(), t.TempDir()
				writeTestFile(t, filepath.Join(source, "README.md"), "selected", 0644)

				plan, err := BuildGeneratePlan(GenerateParameters{PackageManagers: []string{"future"}, ClientRepoRoot: root, TemplateRoot: source, ProjectPath: "libs/new", TemplateKind: "lib"})
				if plan != nil {
					t.Cleanup(func() { _ = plan.close() })
				}

				if err == nil || !strings.Contains(err.Error(), "invalid native root path") {
					t.Fatalf("unsafe claim accepted: %v", err)
				}
				stages, err := filepath.Glob(filepath.Join(root, "libs/.premise-stage-*"))
				if err != nil || len(stages) != 0 {
					t.Fatalf("stages remain after failed planning: %v %v", stages, err)
				}
			})
		}
	})

	t.Run("when a provider returns duplicate or unclaimed outputs", func(t *testing.T) {
		for _, path := range []string{"future.lock", "../outside", "unknown.lock"} {
			t.Run("it rejects "+path, func(t *testing.T) {
				provider := workspaceGenerationProvider{testPackageManager: testPackageManager{id: "future"}, prepare: func(PackageGenerationContext) ([]MergeEntry, error) {
					entry := MergeEntry{Path: path, Output: PathValue{Present: true, Kind: "file", Data: []byte("native")}}
					return []MergeEntry{entry, entry}, nil
				}}
				useTestPackageManagers(t, provider)
				root, source := t.TempDir(), t.TempDir()
				writeTestFile(t, filepath.Join(source, "README.md"), "selected", 0644)

				plan, err := BuildGeneratePlan(GenerateParameters{PackageManagers: []string{"future"}, ClientRepoRoot: root, TemplateRoot: source, ProjectPath: "libs/new", TemplateKind: "lib"})
				if plan != nil {
					t.Cleanup(func() { _ = plan.close() })
				}

				if err == nil {
					t.Fatal("invalid provider outputs accepted")
				}
				if _, err := os.Stat(filepath.Join(root, "libs/new")); !os.IsNotExist(err) {
					t.Fatalf("destination exists: %v", err)
				}
			})
		}
	})

}

// Private helpers ------------------------------------------------------------

func (provider workspaceGenerationProvider) GenerationRootFiles() []string {
	if provider.paths != nil {
		return provider.paths
	}
	return []string{"future.lock"}
}
func (provider workspaceGenerationProvider) PrepareGeneration(request PackageGenerationContext) ([]MergeEntry, error) {
	if provider.prepare != nil {
		return provider.prepare(request)
	}
	return nil, nil
}
